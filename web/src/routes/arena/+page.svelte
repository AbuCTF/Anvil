<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { API_BASE } from '$lib/config';
	import LineChart from '$lib/components/LineChart.svelte';
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

	const SLA: Record<string, { dot: string; cell: string; icon: string; label: string }> = {
		OK: { dot: 'bg-emerald-500', cell: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400', icon: 'mdi:check-bold', label: 'Up' },
		DOWN: { dot: 'bg-red-500', cell: 'bg-red-500/15 border-red-500/30 text-red-400', icon: 'mdi:close-thick', label: 'Down' },
		FAULTY: { dot: 'bg-amber-500', cell: 'bg-amber-500/15 border-amber-500/30 text-amber-400', icon: 'mdi:alert', label: 'Faulty' },
		RECOVERING: { dot: 'bg-sky-500', cell: 'bg-sky-500/15 border-sky-500/30 text-sky-400', icon: 'mdi:refresh', label: 'Recovering' },
		FLAG_NOT_FOUND: { dot: 'bg-orange-500', cell: 'bg-orange-500/15 border-orange-500/30 text-orange-400', icon: 'mdi:flag-remove', label: 'Flag missing' },
		UNKNOWN: { dot: 'bg-stone-600', cell: 'bg-stone-800/50 border-stone-700 text-stone-500', icon: 'mdi:minus', label: 'No data' }
	};
	const slaStyle = (s: string) => SLA[s] ?? SLA.UNKNOWN;
	const CAT: Record<string, string> = {
		pwn: '#f43f5e', web: '#38bdf8', crypto: '#a78bfa', rev: '#fb923c', forensics: '#34d399', misc: '#94a3b8'
	};

	interface ArenaState {
		status: GameStatus;
		hills: Hill[];
		standings: Standing[];
		services: MatrixService[];
		rows: MatrixRow[];
		events: GameEvent[];
		history: HistorySeries[];
	}

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
				// Transient (rate-limit / 5xx): keep the last good board and retry.
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
				color: `hsl(${teamHue(h.team_id)} 70% 55%)`,
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

	function teamHue(key: string): number {
		let h = 0;
		for (let i = 0; i < key.length; i++) h = (Math.imul(h, 31) + key.charCodeAt(i)) >>> 0;
		return h % 360;
	}
	function teamColor(key: string | null | undefined) {
		if (!key) return { bg: 'hsl(30 6% 12%)', border: 'hsl(30 6% 26%)', text: 'hsl(30 6% 62%)', dot: 'hsl(30 6% 45%)' };
		const hue = teamHue(key);
		return {
			bg: `hsl(${hue} 55% 16%)`,
			border: `hsl(${hue} 70% 48%)`,
			text: `hsl(${hue} 85% 82%)`,
			dot: `hsl(${hue} 70% 55%)`
		};
	}
	const fmt = (n: number | null | undefined) => (typeof n === 'number' && Number.isFinite(n) ? n.toFixed(1) : '—');
	function ago(at: number) {
		const s = Math.max(0, Math.floor(now / 1000) - at);
		if (s < 60) return `${s}s`;
		const m = Math.floor(s / 60);
		if (m < 60) return `${m}m`;
		return `${Math.floor(m / 60)}h`;
	}
	const upCount = (r: MatrixRow) => r.cells.filter((c) => c.status === 'OK').length;
</script>

<svelte:head>
	<title>Arena - Anvil</title>
</svelte:head>

<div class="max-w-[1600px] mx-auto px-4 sm:px-6 lg:px-8 py-6">
	{#if loading}
		<div class="flex items-center justify-center py-24">
			<Icon icon="mdi:loading" class="w-10 h-10 text-amber-500 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-red-500/10 border border-red-500/20 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle" class="w-12 h-12 text-red-500 mx-auto mb-4" />
			<p class="text-red-400">{error}</p>
			<p class="text-stone-500 text-sm mt-2">Retrying every {POLL_MS / 1000}s…</p>
		</div>
	{:else if !gameActive}
		<div class="flex flex-col items-center justify-center py-24 text-center">
			<Icon icon="mdi:flag-off-outline" class="w-20 h-20 text-stone-700 mb-5" />
			<h1 class="text-2xl font-bold text-white mb-2">Game not active</h1>
			<p class="text-stone-500">The finals game is currently offline. This board updates automatically.</p>
		</div>
	{:else}
		<!-- Status bar -->
		<div class="flex flex-wrap items-center justify-between gap-4 mb-6">
			<div>
				<h1 class="text-2xl sm:text-3xl font-bold text-white">Arena</h1>
				<p class="text-stone-500 text-sm mt-0.5">Attack · Defense · King of the Hill — live</p>
			</div>
			<div class="flex items-center gap-3">
				<div class="flex items-center gap-2 bg-stone-900/70 border border-stone-800 rounded-lg px-4 py-2">
					<Icon icon="mdi:timer-sand" class="w-5 h-5 text-amber-500" />
					<span class="text-stone-400 text-sm">Tick</span>
					<span class="text-white font-bold text-lg tabular-nums">{status?.tick ?? '—'}</span>
				</div>
				<div class="flex items-center gap-2 bg-stone-900/70 border border-stone-800 rounded-lg px-4 py-2">
					<Icon icon="mdi:crown" class="w-5 h-5 text-amber-500" />
					<span class="text-stone-400 text-sm">Round</span>
					<span class="text-white font-bold text-lg tabular-nums">{status?.round ?? '—'}</span>
				</div>
				<div class="hidden sm:flex items-center gap-2 text-stone-600 text-xs">
					<span class="w-2 h-2 rounded-full bg-green-500 animate-pulse"></span>
					live · {POLL_MS / 1000}s
				</div>
			</div>
		</div>

		<!-- Control map -->
		<div class="mb-6" style="perspective: 1200px;">
			{#if hills.length === 0}
				<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-10 text-center text-stone-500">No hills configured.</div>
			{:else}
				<div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-4">
					{#each hills as hill (hill.hill_id)}
						{@const c = teamColor(hill.controller)}
						{@const contested = !!hill.controller}
						<div
							class="rounded-xl border-2 p-5 min-h-[7.5rem] flex flex-col justify-between transition-shadow duration-300 {flash.has(hill.hill_id) ? 'ring-4 ring-white/60 shadow-lg' : ''}"
							style="background: {c.bg}; border-color: {c.border};"
						>
							<div class="flex items-center justify-between gap-2">
								<span class="text-sm font-medium text-stone-300/90 truncate">{hill.name}</span>
								<Icon icon={contested ? 'mdi:crown' : 'mdi:crown-outline'} class="w-5 h-5 shrink-0" style="color: {c.dot};" />
							</div>
							{#key hill.controller ?? '__none__'}
								<div class="flip-in" style="transform-origin: center;">
									{#if contested}
										<div class="text-xl sm:text-2xl font-extrabold leading-tight truncate" style="color: {c.text};">{hill.controller}</div>
										<div class="text-[0.7rem] uppercase tracking-wider text-stone-400/70 mt-0.5">holds</div>
									{:else}
										<div class="text-xl sm:text-2xl font-extrabold leading-tight text-stone-500">Uncontested</div>
										<div class="text-[0.7rem] uppercase tracking-wider text-stone-600 mt-0.5">open</div>
									{/if}
								</div>
							{/key}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<!-- SLA matrix + event feed -->
		<div class="grid lg:grid-cols-3 gap-6 mb-6">
			<div class="lg:col-span-2 bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:grid" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Service status</h2>
					<span class="text-stone-500 text-sm ml-auto">latest tick</span>
				</div>
				<div class="overflow-x-auto">
					{#if matrixRows.length === 0}
						<div class="px-6 py-10 text-center text-stone-500">No service checks yet.</div>
					{:else}
						<table class="w-full min-w-[560px] border-separate border-spacing-0">
							<thead>
								<tr>
									<th class="sticky left-0 z-10 bg-stone-900/50 px-4 py-2.5 text-left text-xs font-medium text-stone-400">Team</th>
									{#each matrixServices as s}
										<th class="px-2 py-2.5 text-center text-xs font-medium text-stone-300">
											<span class="inline-flex items-center gap-1.5">
												<span class="w-2 h-2 rounded-full" style="background: {CAT[s.category] ?? '#94a3b8'};"></span>
												<span class="truncate">{s.name}</span>
											</span>
										</th>
									{/each}
									<th class="px-3 py-2.5 text-right text-xs font-medium text-stone-500">Up</th>
								</tr>
							</thead>
							<tbody>
								{#each matrixRows as r (r.team_id)}
									{@const tc = teamColor(r.team_id)}
									<tr class="border-t border-stone-800/60">
										<td class="sticky left-0 z-10 bg-stone-950/80 px-4 py-2 whitespace-nowrap border-t border-stone-800/60">
											<div class="flex items-center gap-2">
												<span class="text-stone-500 text-xs tabular-nums w-4 text-right">{r.rank ?? '—'}</span>
												<span class="w-2.5 h-2.5 rounded-full shrink-0" style="background: {tc.dot};"></span>
												<a href="/scoreboard" class="text-stone-200 text-sm font-medium truncate max-w-[140px] hover:text-amber-400 transition">{r.team}</a>
											</div>
										</td>
										{#each r.cells as cell}
											{@const st = slaStyle(cell.status)}
											<td class="px-2 py-2 text-center">
												<span
													class="inline-flex items-center justify-center w-7 h-7 rounded border {st.cell}"
													title="{st.label}{cell.latency_ms != null ? ` · ${cell.latency_ms}ms` : ''}"
												>
													<Icon icon={st.icon} class="w-4 h-4" />
												</span>
											</td>
										{/each}
										<td class="px-3 py-2 text-right text-xs tabular-nums text-stone-400">{upCount(r)}/{r.cells.length}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{/if}
				</div>
				<div class="px-5 py-3 border-t border-stone-800 flex flex-wrap gap-x-4 gap-y-1.5">
					{#each ['OK', 'DOWN', 'FAULTY', 'RECOVERING', 'FLAG_NOT_FOUND'] as s}
						<span class="inline-flex items-center gap-1.5 text-xs text-stone-400">
							<span class="w-2.5 h-2.5 rounded-full {slaStyle(s).dot}"></span>{slaStyle(s).label}
						</span>
					{/each}
				</div>
			</div>

			<!-- Event feed -->
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden flex flex-col">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:sword-cross" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Captures</h2>
					<span class="text-stone-500 text-sm ml-auto tabular-nums">{events.length}</span>
				</div>
				<div class="p-2 overflow-y-auto max-h-[22rem]">
					{#if events.length === 0}
						<div class="px-4 py-8 text-center text-stone-500 text-sm">No captures yet.</div>
					{:else}
						<ul class="space-y-1">
							{#each events as e (eventKey(e))}
								{@const ac = teamColor(e.attacker)}
								<li
									class="flex items-center gap-2 rounded-lg px-3 py-2 text-sm transition-colors {flashEvents.has(eventKey(e)) ? 'bg-amber-500/10' : 'hover:bg-stone-800/40'}"
								>
									<span class="text-[0.65rem] tabular-nums text-stone-600 w-8 shrink-0">t{e.tick}</span>
									<div class="min-w-0 flex-1">
										<span class="font-medium truncate" style="color: {ac.text};">{e.attacker}</span>
										<Icon icon="mdi:arrow-right-thin" class="inline w-4 h-4 text-stone-600 align-middle" />
										<span class="text-stone-400 truncate">{e.victim}</span>
									</div>
									<span class="text-[0.65rem] px-1.5 py-0.5 rounded bg-stone-800 text-stone-400 shrink-0">{e.service}</span>
									<span class="text-[0.65rem] tabular-nums text-stone-600 w-8 text-right shrink-0">{ago(e.at)}</span>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</div>
		</div>

		<!-- Score over time -->
		{#if raceSeries.length}
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden mb-6">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:chart-line" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Score over time</h2>
				</div>
				<div class="p-4">
					<LineChart series={raceSeries} height={300} />
					<div class="flex flex-wrap gap-x-4 gap-y-1 mt-3">
						{#each raceSeries as s}
							<span class="inline-flex items-center gap-1.5 text-xs text-stone-400">
								<span class="w-2.5 h-2.5 rounded-full" style="background: {s.color};"></span>{s.label}
							</span>
						{/each}
					</div>
				</div>
			</div>
		{/if}

		<!-- Standings -->
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
			<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
				<Icon icon="mdi:trophy" class="w-5 h-5 text-amber-500" />
				<h2 class="text-lg font-semibold text-white">Standings</h2>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full min-w-[760px]">
					<thead>
						<tr class="border-b border-stone-800 text-stone-400 text-sm">
							<th class="px-4 sm:px-6 py-3 text-left font-medium w-16">Rank</th>
							<th class="px-4 sm:px-6 py-3 text-left font-medium">Team</th>
							<th class="px-4 sm:px-6 py-3 text-left font-medium hidden lg:table-cell w-40">Breakdown</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Attack</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Defense</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">SLA</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">KotH</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Total</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-800">
						{#each standings as team, i (team.team_id)}
							{@const c = teamColor(team.team_id || team.team)}
							{@const sum = Math.max(1, team.attack + team.defense + team.sla + team.koth)}
							<tr class="hover:bg-stone-800/40 transition-colors {i === 0 ? 'bg-amber-500/5' : ''}">
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-white font-bold tabular-nums">{team.rank ?? '—'}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap">
									<div class="flex items-center gap-2.5">
										<span class="w-3 h-3 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="text-white font-medium truncate max-w-[220px]">{team.team}</span>
									</div>
								</td>
								<td class="px-4 sm:px-6 py-3 hidden lg:table-cell">
									<div class="flex h-2 w-36 overflow-hidden rounded-full bg-stone-800">
										<div style="width: {(team.attack / sum) * 100}%; background: #f43f5e;" title="Attack {fmt(team.attack)}"></div>
										<div style="width: {(team.defense / sum) * 100}%; background: #38bdf8;" title="Defense {fmt(team.defense)}"></div>
										<div style="width: {(team.sla / sum) * 100}%; background: #34d399;" title="SLA {fmt(team.sla)}"></div>
										<div style="width: {(team.koth / sum) * 100}%; background: #f59e0b;" title="KotH {fmt(team.koth)}"></div>
									</div>
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.attack)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right tabular-nums {team.defense < 0 ? 'text-red-400' : 'text-stone-300'}">{fmt(team.defense)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.sla)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.koth)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-amber-500 font-bold tabular-nums">{fmt(team.total)}</td>
							</tr>
						{/each}
						{#if standings.length === 0}
							<tr><td colspan="8" class="px-6 py-10 text-center text-stone-500">No standings yet.</td></tr>
						{/if}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>

<style>
	.flip-in {
		animation: flipIn 0.5s ease-out;
	}
	@keyframes flipIn {
		0% {
			transform: rotateX(90deg);
			opacity: 0;
		}
		60% {
			transform: rotateX(-12deg);
			opacity: 1;
		}
		100% {
			transform: rotateX(0);
			opacity: 1;
		}
	}
</style>
