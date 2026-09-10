<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { API_BASE } from '$lib/config';
	import LineChart from '$lib/components/LineChart.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { teamColor as seriesColor } from '$lib/rank';
	import type { Series } from '$lib/chart/path';

	interface Standing {
		rank: number | null;
		team_id: string;
		team: string;
		attack: number;
		defense: number;
		sla: number;
		koth: number;
		total: number;
	}
	interface Hill {
		hill_id: string;
		name: string;
		controller?: string | null;
	}
	interface GameStatus {
		tick: number;
		round: number;
		tick_interval_seconds: number;
	}
	interface HistorySeries {
		team_id: string;
		team: string;
		points: { x: number; y: number }[];
	}
	interface MatrixService {
		service_id: string;
		name: string;
		category: string;
		tier: string;
	}
	interface MatrixCell {
		status: string;
		latency_ms?: number;
	}
	interface MatrixRow {
		team_id: string;
		team: string;
		rank?: number;
		cells: MatrixCell[];
	}
	interface GameEvent {
		tick: number;
		attacker: string;
		victim: string;
		service: string;
		at: number;
	}
	interface ArenaState {
		status: GameStatus;
		hills: Hill[];
		standings: Standing[];
		services: MatrixService[];
		rows: MatrixRow[];
		events: GameEvent[];
		history: HistorySeries[];
	}

	const POLL_MS = 5000;

	let standings: Standing[] = [];
	let hills: Hill[] = [];
	let status: GameStatus | null = null;
	let raceSeries: Series[] = [];
	let matrixServices: MatrixService[] = [];
	let matrixRows: MatrixRow[] = [];
	let events: GameEvent[] = [];
	let now = Date.now();

	let loading = true;
	let error = '';
	let gameActive = true;

	let initialized = false;
	let prevControllers: Record<string, string | null> = {};
	let flash = new Set<string>();
	let seenEvents = new Set<string>();
	let flashEvents = new Set<string>();

	let inFlight = false;
	let timer: ReturnType<typeof setInterval>;

	// Muted status colors — only a genuine problem is meant to draw the eye.
	const SLA: Record<string, { color: string; label: string }> = {
		OK: { color: '#4b7355', label: 'Up' },
		DOWN: { color: '#b0453a', label: 'Down' },
		FAULTY: { color: '#9c7a30', label: 'Faulty' },
		RECOVERING: { color: '#3f6a86', label: 'Recovering' },
		FLAG_NOT_FOUND: { color: '#9c5a30', label: 'Flag missing' },
		UNKNOWN: { color: '#3a3735', label: 'No data' }
	};
	const slaStyle = (s: string) => SLA[s] ?? SLA.UNKNOWN;
	const CAT: Record<string, string> = {
		pwn: '#a15a52',
		web: '#5a7a92',
		crypto: '#7d6f9c',
		rev: '#a1774a',
		forensics: '#5a8060',
		misc: '#6b6560'
	};

	async function load() {
		if (inFlight) return;
		inFlight = true;
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/state`, { headers: { 'Content-Type': 'application/json' } });
			if (res.status === 404) {
				gameActive = false;
				error = '';
				return;
			}
			if (!res.ok) {
				if (!initialized) error = `HTTP ${res.status}`;
				return;
			}
			const s = (await res.json()) as ArenaState & { error?: string };
			if (s.error) {
				gameActive = false;
				error = '';
				return;
			}

			gameActive = true;
			now = Date.now();
			status = s.status ?? status;
			applyHills(s.hills ?? []);
			standings = s.standings ?? [];
			matrixServices = s.services ?? [];
			matrixRows = s.rows ?? [];
			applyEvents(s.events ?? []);
			raceSeries = (s.history ?? []).map((h) => ({
				label: h.team,
				color: seriesColor(h.team_id),
				points: h.points
			}));
			initialized = true;
			error = '';
		} catch (e) {
			if (!initialized) error = e instanceof Error ? e.message : 'Failed to load game state';
		} finally {
			loading = false;
			inFlight = false;
		}
	}

	function applyHills(next: Hill[]) {
		const changed = new Set<string>();
		for (const h of next) {
			const controller = h.controller ?? null;
			if (initialized && prevControllers[h.hill_id] !== controller) changed.add(h.hill_id);
			prevControllers[h.hill_id] = controller;
		}
		hills = next;
		if (changed.size) {
			flash = new Set([...flash, ...changed]);
			for (const id of changed) {
				setTimeout(() => {
					flash.delete(id);
					flash = new Set(flash);
				}, 2400);
			}
		}
	}

	function eventKey(e: GameEvent) {
		return `${e.tick}:${e.attacker}:${e.victim}:${e.service}`;
	}
	function applyEvents(next: GameEvent[]) {
		if (initialized) {
			const fresh = next.filter((e) => !seenEvents.has(eventKey(e))).map(eventKey);
			if (fresh.length) {
				flashEvents = new Set([...flashEvents, ...fresh]);
				for (const k of fresh)
					setTimeout(() => {
						flashEvents.delete(k);
						flashEvents = new Set(flashEvents);
					}, 2400);
			}
		}
		for (const e of next) seenEvents.add(eventKey(e));
		events = next;
	}

	onMount(() => {
		load();
		timer = setInterval(load, POLL_MS);
		return () => clearInterval(timer);
	});

	function teamColor(key: string | null | undefined) {
		if (!key) return { dot: '#57534c', text: '#837e75' };
		const c = seriesColor(key);
		return { dot: c, text: c };
	}
	const fmt = (n: number | null | undefined) => (typeof n === 'number' && Number.isFinite(n) ? n.toFixed(0) : '—');
	function ago(at: number) {
		const s = Math.max(0, Math.floor(now / 1000) - at);
		if (s < 60) return `${s}s`;
		const m = Math.floor(s / 60);
		if (m < 60) return `${m}m`;
		return `${Math.floor(m / 60)}h`;
	}
	const upCount = (r: MatrixRow) => r.cells.filter((c) => c.status === 'OK').length;
	$: leaderIdx = standings.length ? raceSeries.findIndex((s) => s.label === standings[0].team) : -1;
	$: bd = { attack: '#9e574f', defense: '#57748c', sla: '#57805f', koth: '#b0862f' };
</script>

<svelte:head>
	<title>Arena - Anvil</title>
</svelte:head>

<div class="max-w-[1500px] mx-auto px-4 sm:px-6 lg:px-8 py-6">
	{#if loading}
		<div class="flex items-center justify-center py-24">
			<Icon icon="mdi:loading" class="w-9 h-9 text-amber-500/80 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-stone-900/50 border border-stone-800 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle-outline" class="w-10 h-10 text-stone-500 mx-auto mb-3" />
			<p class="text-stone-400">{error}</p>
			<p class="text-stone-600 text-sm mt-1">Retrying every {POLL_MS / 1000}s…</p>
		</div>
	{:else if !gameActive}
		<div class="flex flex-col items-center justify-center py-24 text-center">
			<Icon icon="mdi:flag-off-outline" class="w-16 h-16 text-stone-700 mb-4" />
			<h1 class="text-xl font-semibold text-stone-200 mb-1">Game not active</h1>
			<p class="text-stone-500">The finals board comes online when the game starts.</p>
		</div>
	{:else}
		<PageHeader title="Arena" subtitle="Attack · Defense · King of the Hill">
			<div slot="actions" class="flex items-center gap-2 text-sm">
				<div class="flex items-center gap-2 bg-stone-900/60 border border-stone-800 rounded-md px-3 py-1.5">
					<span class="text-stone-500 text-xs">Tick</span>
					<span class="text-stone-100 font-semibold tabular-nums">{status?.tick ?? '—'}</span>
				</div>
				<div class="flex items-center gap-2 bg-stone-900/60 border border-stone-800 rounded-md px-3 py-1.5">
					<span class="text-stone-500 text-xs">Round</span>
					<span class="text-stone-100 font-semibold tabular-nums">{status?.round ?? '—'}</span>
				</div>
				<span class="hidden sm:inline-flex items-center gap-1.5 text-stone-600 text-xs pl-1">
					<span class="w-1.5 h-1.5 rounded-full bg-amber-500/70 animate-pulse"></span>live
				</span>
			</div>
		</PageHeader>

		<!-- King of the Hill -->
		{#if hills.length}
			<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden mb-6">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">King of the Hill</h2>
					<span class="text-stone-600 text-xs ml-auto tabular-nums">{hills.length} hills</span>
				</div>
				<div class="grid grid-cols-2 sm:grid-cols-4">
					{#each hills as hill (hill.hill_id)}
						{@const c = teamColor(hill.controller)}
						{@const held = !!hill.controller}
						<div
							class="px-5 py-4 border-stone-800/60 border-t sm:border-t-0 sm:border-l sm:first:border-l-0 transition-colors {flash.has(
								hill.hill_id
							)
								? 'bg-amber-500/5'
								: ''}"
						>
							<div class="flex items-center gap-1.5 text-xs text-stone-500 mb-2">
								<Icon icon={held ? 'mdi:crown' : 'mdi:crown-outline'} class="w-3.5 h-3.5 {held ? 'text-amber-500/70' : 'text-stone-700'}" />
								{hill.name}
							</div>
							{#key hill.controller ?? '__none__'}
								<div class="fade-in flex items-center gap-2">
									{#if held}
										<span class="w-2 h-2 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="text-lg font-semibold text-stone-100 truncate">{hill.controller}</span>
									{:else}
										<span class="text-lg font-semibold text-stone-600">Open</span>
									{/if}
								</div>
							{/key}
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<!-- Service status + captures -->
		<div class="grid lg:grid-cols-3 gap-6 mb-6">
			<div class="lg:col-span-2 bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Service status</h2>
					<span class="text-stone-600 text-xs ml-auto">latest tick</span>
				</div>
				<div class="overflow-x-auto">
					{#if matrixRows.length === 0}
						<div class="px-4 py-8 text-center text-stone-500 text-sm">No service checks yet.</div>
					{:else}
						<table class="w-full min-w-[560px] border-separate border-spacing-0">
							<thead>
								<tr>
									<th class="sticky left-0 z-10 bg-stone-900/40 px-4 py-2 text-left text-[0.7rem] uppercase tracking-wider font-medium text-stone-500">
										Team
									</th>
									{#each matrixServices as s}
										<th class="px-2 py-2 text-center text-xs font-medium text-stone-400">
											<span class="inline-flex items-center gap-1.5">
												<span class="w-1.5 h-1.5 rounded-full" style="background: {CAT[s.category] ?? '#6b6560'};"></span>
												{s.name}
											</span>
										</th>
									{/each}
									<th class="px-3 py-2 text-right text-[0.7rem] uppercase tracking-wider font-medium text-stone-600">Up</th>
								</tr>
							</thead>
							<tbody>
								{#each matrixRows as r (r.team_id)}
									{@const tc = teamColor(r.team_id)}
									<tr>
										<td class="sticky left-0 z-10 bg-stone-950/70 px-4 py-1.5 whitespace-nowrap border-t border-stone-800/60">
											<div class="flex items-center gap-2">
												<span class="text-stone-600 text-xs tabular-nums w-5 text-right">{r.rank ?? '—'}</span>
												<span class="w-2 h-2 rounded-full shrink-0" style="background: {tc.dot};"></span>
												<a href="/scoreboard" class="text-stone-300 text-sm truncate max-w-[140px] hover:text-amber-400 transition">{r.team}</a>
											</div>
										</td>
										{#each r.cells as cell}
											{@const st = slaStyle(cell.status)}
											<td class="px-2 py-1.5 text-center border-t border-stone-800/60">
												<span
													class="block w-6 h-6 rounded-sm mx-auto"
													style="background: {st.color};"
													title="{r.team} · {st.label}{cell.latency_ms != null ? ` · ${cell.latency_ms}ms` : ''}"
												></span>
											</td>
										{/each}
										<td class="px-3 py-1.5 text-right text-xs tabular-nums text-stone-500 border-t border-stone-800/60">
											{upCount(r)}/{r.cells.length}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{/if}
				</div>
				<div class="px-4 py-2.5 border-t border-stone-800 flex flex-wrap gap-x-4 gap-y-1.5">
					{#each ['OK', 'DOWN', 'FAULTY', 'RECOVERING', 'FLAG_NOT_FOUND'] as s}
						<span class="inline-flex items-center gap-1.5 text-[0.7rem] text-stone-500">
							<span class="w-2.5 h-2.5 rounded-sm" style="background: {slaStyle(s).color};"></span>{slaStyle(s).label}
						</span>
					{/each}
				</div>
			</div>

			<!-- Captures -->
			<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden flex flex-col">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Captures</h2>
					<span class="text-stone-600 text-xs ml-auto tabular-nums">{events.length}</span>
				</div>
				<div class="p-1.5 overflow-y-auto max-h-[21rem]">
					{#if events.length === 0}
						<div class="px-4 py-8 text-center text-stone-500 text-sm">No captures yet.</div>
					{:else}
						<ul>
							{#each events as e (eventKey(e))}
								{@const ac = teamColor(e.attacker)}
								<li
									class="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm transition-colors {flashEvents.has(eventKey(e))
										? 'bg-amber-500/5'
										: ''}"
								>
									<span class="text-[0.6rem] tabular-nums text-stone-700 w-7 shrink-0">t{e.tick}</span>
									<div class="min-w-0 flex-1 truncate">
										<span style="color: {ac.text};">{e.attacker}</span>
										<span class="text-stone-700 px-0.5">→</span>
										<span class="text-stone-500">{e.victim}</span>
									</div>
									<span class="text-[0.6rem] px-1.5 py-0.5 rounded bg-stone-800/80 text-stone-500 shrink-0">{e.service}</span>
									<span class="text-[0.6rem] tabular-nums text-stone-700 w-7 text-right shrink-0">{ago(e.at)}</span>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</div>
		</div>

		<!-- Score over time -->
		{#if raceSeries.length}
			<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden mb-6">
				<div class="px-4 py-3 border-b border-stone-800">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Score over time</h2>
				</div>
				<div class="p-4">
					<LineChart series={raceSeries} height={280} curve="step" emphasize={leaderIdx} xFormat={(x) => 't' + Math.round(x)} />
				</div>
			</div>
		{/if}

		<!-- Standings -->
		<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden">
			<div class="px-4 py-3 border-b border-stone-800">
				<h2 class="text-[0.95rem] font-semibold text-stone-200">Standings</h2>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full min-w-[720px] text-sm">
					<thead>
						<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider">
							<th class="px-4 py-2.5 text-left font-medium w-12">#</th>
							<th class="px-4 py-2.5 text-left font-medium">Team</th>
							<th class="px-4 py-2.5 text-left font-medium hidden lg:table-cell w-36">Breakdown</th>
							<th class="px-4 py-2.5 text-right font-medium">Atk</th>
							<th class="px-4 py-2.5 text-right font-medium">Def</th>
							<th class="px-4 py-2.5 text-right font-medium">SLA</th>
							<th class="px-4 py-2.5 text-right font-medium">KotH</th>
							<th class="px-4 py-2.5 text-right font-medium">Total</th>
						</tr>
					</thead>
					<tbody>
						{#each standings as team, i (team.team_id)}
							{@const c = teamColor(team.team_id || team.team)}
							{@const sum = Math.max(1, team.attack + team.defense + team.sla + team.koth)}
							<tr class="border-t border-stone-800/60 hover:bg-stone-800/20 transition-colors">
								<td class="px-4 py-2.5 whitespace-nowrap text-stone-300 font-semibold tabular-nums">{team.rank ?? '—'}</td>
								<td class="px-4 py-2.5 whitespace-nowrap">
									<div class="flex items-center gap-2.5">
										<span class="w-2 h-2 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="text-stone-200 truncate max-w-[220px]">{team.team}</span>
									</div>
								</td>
								<td class="px-4 py-2.5 hidden lg:table-cell">
									<div class="flex h-1.5 w-32 overflow-hidden rounded-full bg-stone-800/80">
										<div style="width: {(team.attack / sum) * 100}%; background: {bd.attack};" title="Attack {fmt(team.attack)}"></div>
										<div style="width: {(team.defense / sum) * 100}%; background: {bd.defense};" title="Defense {fmt(team.defense)}"></div>
										<div style="width: {(team.sla / sum) * 100}%; background: {bd.sla};" title="SLA {fmt(team.sla)}"></div>
										<div style="width: {(team.koth / sum) * 100}%; background: {bd.koth};" title="KotH {fmt(team.koth)}"></div>
									</div>
								</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.attack)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right tabular-nums {team.defense < 0 ? 'text-red-400/80' : 'text-stone-400'}">{fmt(team.defense)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.sla)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.koth)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-amber-500/90 font-semibold tabular-nums">{fmt(team.total)}</td>
							</tr>
						{/each}
						{#if standings.length === 0}
							<tr><td colspan="8" class="px-4 py-8 text-center text-stone-500">No standings yet.</td></tr>
						{/if}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>

<style>
	.fade-in {
		animation: fadeIn 0.4s ease-out;
	}
	@keyframes fadeIn {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
</style>
