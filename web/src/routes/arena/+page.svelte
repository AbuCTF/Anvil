<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { API_BASE } from '$lib/config';
	import { auth } from '$stores/auth';
	import ArenaChart, { type HistorySeries } from '$lib/components/ArenaChart.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import CopyText from '$lib/components/CopyText.svelte';
	import { vpnBoxAddr } from '$lib/finals-access';
	import { teamColor as seriesColor } from '$lib/rank';
	import { pts, type ScoreShare, type ScoreWeights } from '$lib/scoring';

	interface Standing {
		rank: number | null;
		team_id: string;
		team: string;
		attack: number;
		defense: number;
		sla: number;
		koth: number;
		jeopardy?: number;
		total: number;
		share?: ScoreShare;
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
		id: string;
		tick: number;
		attacker: string;
		victim: string;
		service: string;
		at: number;
	}
	interface ArenaState {
		active: boolean;
		frozen?: boolean;
		status: GameStatus;
		hills: Hill[];
		standings: Standing[];
		services: MatrixService[];
		rows: MatrixRow[];
		events: GameEvent[];
		history: HistorySeries[];
		koth_history?: HistorySeries[];
		weights?: ScoreWeights;
	}

	// /arena/me + /arena/me/status, signed-in team only
	interface MyTeam {
		team_number: number | null;
		vulnbox_ip: string | null;
	}
	interface MyService {
		service_id: string;
		name: string;
		port: number | null;
	}

	const POLL_MS = 5000;
	const IDLE_POLL_MS = 30000;
	const REQUEST_TIMEOUT_MS = 10000;

	let standings: Standing[] = [];
	let weights: ScoreWeights | null = null;
	let hills: Hill[] = [];
	let status: GameStatus | null = null;
	let history: HistorySeries[] = [];
	let kothHistory: HistorySeries[] = [];
	let matrixServices: MatrixService[] = [];
	let matrixRows: MatrixRow[] = [];
	let events: GameEvent[] = [];
	let myTeam: MyTeam | null = null;
	let myServices: MyService[] = [];
	let myTried = false;
	let now = Date.now();

	let loading = true;
	let error = '';
	let refreshError = '';
	let active = false;
	let frozen = false;

	let initialized = false;
	let hasLiveBaseline = false;
	let prevControllers: Record<string, string | null> = {};
	let flash = new Set<string>();
	let seenEvents = new Set<string>();
	let flashEvents = new Set<string>();
	let flashTimers = new Map<string, ReturnType<typeof setTimeout>>();

	let inFlight = false;
	let timer: ReturnType<typeof setTimeout>;
	let requestController: AbortController | null = null;
	let disposed = false;

	// muted status colors - only a genuine problem is meant to draw the eye.
	const SLA: Record<string, { color: string; label: string }> = {
		OK: { color: '#4b7355', label: 'Up' },
		DOWN: { color: '#b0453a', label: 'Down' },
		FAULTY: { color: '#9c7a30', label: 'Faulty' },
		RECOVERING: { color: '#3f6a86', label: 'Recovering' },
		FLAG_NOT_FOUND: { color: '#9c5a30', label: 'Flag missing' },
		UNKNOWN: { color: '#3a3735', label: 'No data' }
	};
	const slaStyle = (s: string) => SLA[s] ?? SLA.UNKNOWN;
	const LEGEND = ['OK', 'DOWN', 'FAULTY', 'RECOVERING', 'FLAG_NOT_FOUND'];

	async function load() {
		if (inFlight) return;
		inFlight = true;
		const controller = new AbortController();
		requestController = controller;
		let timedOut = false;
		const requestTimeout = setTimeout(() => {
			timedOut = true;
			controller.abort();
		}, REQUEST_TIMEOUT_MS);
		try {
			// send the access token when present so staff can preview the arena before the
			// drop (server grants admins the live state pre-open; players/anon see inactive).
			const previewToken =
				typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
			const res = await fetch(`${API_BASE}/api/v1/arena/state`, {
				signal: controller.signal,
				headers: previewToken ? { Authorization: `Bearer ${previewToken}` } : {}
			});
			if (res.status === 404) {
				applyInactiveState();
				return;
			}
			if (!res.ok) {
				const message = `Arena state unavailable (HTTP ${res.status})`;
				if (initialized && active) refreshError = message;
				else error = message;
				return;
			}
			const cacheStatus = res.headers.get('X-Anvil-Cache');
			const s = (await res.json()) as ArenaState & { error?: string };
			if (s.error || s.active === false) {
				applyInactiveState();
				return;
			}
			applyState(s);
			if (cacheStatus === 'STALE') refreshError = 'Live arena updates are delayed';
		} catch (e) {
			if (disposed || (controller.signal.aborted && !timedOut)) return;
			const message = timedOut
				? 'Arena state request timed out'
				: e instanceof Error
					? e.message
					: 'Failed to load game state';
			if (initialized && active) refreshError = message;
			else error = message;
		} finally {
			clearTimeout(requestTimeout);
			if (requestController === controller) requestController = null;
			loading = false;
			inFlight = false;
		}
	}

	function applyInactiveState() {
		active = false;
		frozen = false;
		status = null;
		standings = [];
		weights = null;
		hills = [];
		matrixServices = [];
		matrixRows = [];
		events = [];
		history = [];
		kothHistory = [];
		prevControllers = {};
		seenEvents = new Set();
		flash = new Set();
		flashEvents = new Set();
		for (const flashTimer of flashTimers.values()) clearTimeout(flashTimer);
		flashTimers.clear();
		hasLiveBaseline = false;
		initialized = true;
		error = '';
		refreshError = '';
	}

	function applyState(s: ArenaState) {
		now = Date.now();
		active = true;
		frozen = !!s.frozen;
		status = s.status ?? status;
		applyHills(s.hills ?? []);
		standings = s.standings ?? [];
		weights = s.weights ?? null;
		matrixServices = s.services ?? [];
		matrixRows = s.rows ?? [];
		applyEvents(s.events ?? []);
		history = s.history ?? [];
		kothHistory = s.koth_history ?? [];
		hasLiveBaseline = true;
		initialized = true;
		error = '';
		refreshError = '';
		if (!myTried && matrixServices.length) void loadMyBox();
	}

	// own box + service ports; a network blip retries on the next poll, a 401 (no team) stops
	async function loadMyBox() {
		myTried = true;
		const token = localStorage.getItem('accessToken');
		if (!token) return;
		const headers = { Authorization: `Bearer ${token}` };
		try {
			const me = await fetch(`${API_BASE}/api/v1/arena/me`, { headers });
			if (!me.ok) return;
			myTeam = await me.json();
			// since = next tick keeps the tick history out, services and ports still come back
			const st = await fetch(`${API_BASE}/api/v1/arena/me/status?since=${(status?.tick ?? 0) + 1}`, { headers });
			if (st.ok) myServices = (await st.json()).services ?? [];
		} catch {
			myTried = false;
		}
	}

	function applyHills(next: Hill[]) {
		const changed = new Set<string>();
		const nextControllers: Record<string, string | null> = {};
		for (const h of next) {
			const controller = h.controller ?? null;
			if (hasLiveBaseline && prevControllers[h.hill_id] !== controller) changed.add(h.hill_id);
			nextControllers[h.hill_id] = controller;
		}
		prevControllers = nextControllers;
		hills = next;
		if (changed.size) {
			flash = new Set([...flash, ...changed]);
			for (const id of changed) {
				const timerKey = `hill:${id}`;
				const previousTimer = flashTimers.get(timerKey);
				if (previousTimer) clearTimeout(previousTimer);
				const flashTimer = setTimeout(() => {
					flash.delete(id);
					flash = new Set(flash);
					flashTimers.delete(timerKey);
				}, 2400);
				flashTimers.set(timerKey, flashTimer);
			}
		}
	}

	function eventKey(e: GameEvent) {
		return e.id;
	}
	function applyEvents(next: GameEvent[]) {
		if (hasLiveBaseline) {
			const fresh = next.filter((e) => !seenEvents.has(eventKey(e))).map(eventKey);
			if (fresh.length) {
				flashEvents = new Set([...flashEvents, ...fresh]);
				for (const k of fresh) {
					const timerKey = `event:${k}`;
					const previousTimer = flashTimers.get(timerKey);
					if (previousTimer) clearTimeout(previousTimer);
					const flashTimer = setTimeout(() => {
						flashEvents.delete(k);
						flashEvents = new Set(flashEvents);
						flashTimers.delete(timerKey);
					}, 2400);
					flashTimers.set(timerKey, flashTimer);
				}
			}
		}
		seenEvents = new Set(next.map(eventKey));
		events = next;
	}

	onMount(() => {
		disposed = false;
		let generation = 0;
		const poll = async (pollGeneration: number) => {
			const joinedInFlight = inFlight;
			await load();
			if (!disposed && pollGeneration === generation && document.visibilityState === 'visible') {
				const delay = joinedInFlight ? 250 : active ? POLL_MS : IDLE_POLL_MS;
				timer = setTimeout(() => poll(pollGeneration), delay);
			}
		};
		const onVisibilityChange = () => {
			generation++;
			clearTimeout(timer);
			requestController?.abort();
			if (document.visibilityState === 'visible') poll(generation);
		};

		poll(++generation);
		document.addEventListener('visibilitychange', onVisibilityChange);
		return () => {
			disposed = true;
			generation++;
			clearTimeout(timer);
			requestController?.abort();
			for (const flashTimer of flashTimers.values()) clearTimeout(flashTimer);
			flashTimers.clear();
			document.removeEventListener('visibilitychange', onVisibilityChange);
		};
	});

	function teamColor(key: string | null | undefined) {
		if (!key) return { dot: '#57534c', text: '#837e75' };
		const c = seriesColor(key);
		return { dot: c, text: c };
	}
	const fmt = (n: number | null | undefined) => (typeof n === 'number' && Number.isFinite(n) ? n.toFixed(0) : '-');
	function ago(at: number) {
		if (!Number.isFinite(at) || at <= 0) return '-';
		const s = Math.max(0, Math.floor(now / 1000) - at);
		if (s < 60) return `${s}s`;
		const m = Math.floor(s / 60);
		if (m < 60) return `${m}m`;
		return `${Math.floor(m / 60)}h`;
	}
	const upCount = (r: MatrixRow) => r.cells.filter((c) => c.status === 'OK').length;
	$: hasUnknown = matrixRows.some((r) => r.cells.some((c) => slaStyle(c.status) === SLA.UNKNOWN));
	const bd = { attack: '#9e574f', defense: '#57748c', sla: '#57805f', koth: '#b0862f' };

	// A/D-only panels (service status, captures, atk/def/sla) render only when there's
	// actual attack-defense activity - i.e. services are registered. A KotH-only event
	// has hills but no services, so it shows a clean KotH board (hills + koth + rounds).
	$: hasAD = matrixServices.length > 0;
	$: isAdmin = $auth.user?.role === 'admin';
	$: showBreakdown = !frozen || isAdmin;
	// finals blend: the score's parts as columns, raw points underneath
	$: parts = weights
		? ([
				...(hasAD ? [{ key: 'ad', label: 'Attack-Defense', short: 'AD' }] : []),
				{ key: 'koth', label: 'King of the Hill', short: 'KotH' },
				...(weights.jeopardy ? [{ key: 'jeopardy', label: 'Jeopardy', short: 'Jeopardy' }] : [])
			] as { key: keyof ScoreWeights; label: string; short: string }[])
		: [];
	const raw = (t: Standing, key: keyof ScoreWeights) =>
		key === 'ad' ? t.attack + t.defense + t.sla : key === 'koth' ? t.koth : t.jeopardy;
	const signed = (n: number | undefined) => (typeof n === 'number' && n > 0 ? `+${pts(n)}` : pts(n));
	$: standingsColumns = !showBreakdown ? 3 : weights ? 3 + parts.length : 4 + (hasAD ? 4 : 0);
	$: myBox = myTeam?.vulnbox_ip ?? (myTeam?.team_number ? vpnBoxAddr(myTeam.team_number) : null);
</script>

<svelte:head>
	<title>Arena - Anvil</title>
</svelte:head>

<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-6">
	{#if loading}
		<div class="flex items-center justify-center py-24">
			<Icon icon="mdi:loading" class="w-9 h-9 text-amber-500/80 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-stone-900/50 border border-stone-800 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle-outline" class="w-10 h-10 text-stone-500 mx-auto mb-3" />
			<p class="text-stone-400">{error}</p>
			<p class="text-stone-600 text-sm mt-1">Retrying every {IDLE_POLL_MS / 1000}s…</p>
		</div>
	{:else}
		<PageHeader title="Arena" compact>
			<div slot="actions" class="flex items-center gap-2 text-sm leading-none">
				{#if active}
					<div class="flex items-center gap-2 bg-stone-900/60 border border-stone-800 rounded-md px-3 py-1.5">
						<span class="optical-label metadata-label text-stone-500">Tick</span>
						<span class="optical-label text-stone-100 font-semibold tabular-nums">{status?.tick ?? '-'}</span>
					</div>
					<div class="flex items-center gap-2 bg-stone-900/60 border border-stone-800 rounded-md px-3 py-1.5">
						<span class="optical-label metadata-label text-stone-500">Round</span>
						<span class="optical-label text-stone-100 font-semibold tabular-nums">{status?.round ?? '-'}</span>
					</div>
					<span
						class="hidden sm:inline-flex items-center gap-1.5 text-xs leading-none pl-1 {refreshError ? 'text-warn' : 'text-stone-600'}"
						title={refreshError || 'Live arena feed'}
					>
						<span class="w-1.5 h-1.5 rounded-full {refreshError ? 'bg-warn' : 'bg-amber-500/70 animate-pulse'}"></span><span class="optical-label">{refreshError ? 'delayed' : 'live'}</span>
					</span>
				{/if}
			</div>
		</PageHeader>

		{#if frozen && !isAdmin}
			<div class="mb-4 overflow-hidden rounded-xl border border-sky-400/15 bg-gradient-to-r from-sky-500/[0.10] via-sky-400/[0.05] to-transparent">
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-sky-400/10 ring-1 ring-sky-400/25">
						<Icon icon="mdi:snowflake" class="h-5 w-5 text-sky-300" />
					</div>
					<div class="min-w-0">
						<p class="text-sm font-semibold tracking-tight text-sky-100">Scoreboard frozen</p>
						<p class="text-xs text-sky-200/60">Standings are locked at the freeze. Final results reveal after prize distribution.</p>
					</div>
				</div>
			</div>
		{:else if frozen && isAdmin}
			<div class="mb-4 inline-flex items-center gap-1.5 rounded-full border border-emerald-400/25 bg-emerald-400/10 px-3 py-1 text-xs font-medium text-emerald-300">
				<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
				Live view (admin)
			</div>
		{/if}

		{#if !active}
			<EmptyState icon="mdi:sword-cross" text="No live arena round is running." />
		{:else}

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
							<div class="flex items-center gap-1.5 text-xs leading-none text-stone-500 mb-2">
								<OpticalIcon icon={held ? 'mdi:crown' : 'mdi:crown-outline'} size={12} box={12} className={held ? 'text-amber-500/70' : 'text-stone-700'} />
								<span class="optical-label">{hill.name}</span>
							</div>
							{#key hill.controller ?? '__none__'}
								<div class="fade-in flex items-center gap-2">
									{#if held}
										<span class="w-2 h-2 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="text-lg font-semibold text-stone-100 truncate leading-none">{hill.controller}</span>
									{:else}
										<span class="text-lg font-semibold text-stone-600 leading-none">Open</span>
									{/if}
								</div>
							{/key}
						</div>
					{/each}
				</div>
			</div>
		{/if}

		{#if hasAD}
		{#if myBox}
			<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden mb-6">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Your box</h2>
					<span class="text-stone-600 text-xs ml-auto">VPN required</span>
				</div>
				<div class="p-4 grid gap-5 lg:grid-cols-3">
					<div class="min-w-0 space-y-3">
						<div>
							<p class="metadata-label text-stone-500 mb-1.5">Address</p>
							<div class="rounded-md border border-stone-800 bg-stone-950/60 px-3 py-2 text-sm">
								<CopyText text={myBox} className="text-stone-200" />
							</div>
						</div>
						<div>
							<p class="metadata-label text-stone-500 mb-1.5">SSH</p>
							<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-950/60 px-3 py-2 text-sm">
								<span class="select-none font-mono text-stone-600">$</span>
								<CopyText text="ssh ubuntu@{myBox}" wrap className="min-w-0 text-stone-200" />
							</div>
						</div>
					</div>
					{#if myServices.length}
						<div class="min-w-0 lg:col-span-2">
							<p class="metadata-label text-stone-500 mb-1.5">Services</p>
							<ul class="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
								{#each myServices as svc (svc.service_id)}
									<li class="flex items-center justify-between gap-3 rounded-md border border-stone-800/60 px-3 py-2">
										<span class="min-w-0 truncate text-stone-300">{svc.name}</span>
										{#if svc.port}
											<CopyText text="{myBox}:{svc.port}" className="shrink-0 text-xs tabular-nums text-stone-400 hover:text-stone-100" />
										{/if}
									</li>
								{/each}
							</ul>
						</div>
					{/if}
				</div>
			</div>
		{/if}
		<div class="grid lg:grid-cols-3 gap-6 mb-6">
			<div class="lg:col-span-2 bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Service status</h2>
				</div>
				{#if matrixRows.length === 0}
					<div class="px-4 py-8 text-center text-stone-500 text-sm">No service checks yet.</div>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full min-w-[21rem] table-fixed border-separate border-spacing-0">
							<colgroup>
								<col class="w-36 sm:w-56" />
								{#each matrixServices as s (s.service_id)}<col />{/each}
								<col class="w-12 sm:w-16" />
							</colgroup>
							<thead>
								<tr>
									<th class="metadata-label solid-cell sticky left-0 z-10 px-3 sm:px-4 py-2.5 text-left text-stone-500">Team</th>
									{#each matrixServices as s, i (s.service_id)}
										<th class="px-1 py-2.5 text-center text-xs font-semibold leading-none tabular-nums text-stone-400" title={s.name}>
											<span class="optical-label">{i + 1}</span>
										</th>
									{/each}
									<th class="metadata-label px-3 sm:px-4 py-2.5 text-right text-stone-500">Up</th>
								</tr>
							</thead>
							<tbody>
								{#each matrixRows as r (r.team_id)}
									{@const tc = teamColor(r.team_id)}
									{@const up = upCount(r)}
									<tr>
										<td class="solid-cell sticky left-0 z-10 px-3 sm:px-4 py-1 border-t border-stone-800/60">
											<div class="flex min-w-0 items-center gap-2 leading-none">
												<span class="optical-label w-5 shrink-0 text-right text-xs tabular-nums text-stone-600">{showBreakdown ? (r.rank ?? '-') : '-'}</span>
												<span class="w-2 h-2 rounded-full shrink-0" style="background: {tc.dot};"></span>
												<a href="/scoreboard" class="optical-label min-w-0 truncate text-xs sm:text-sm text-stone-300 hover:text-amber-400 transition" title={r.team}>{r.team}</a>
											</div>
										</td>
										{#each r.cells as cell, i}
											{@const st = slaStyle(cell.status)}
											<td class="px-1 py-1 border-t border-stone-800/60">
												<span
													class="mx-auto block h-5 w-full max-w-16 rounded-[3px] {st === SLA.UNKNOWN ? 'bg-stone-800' : ''}"
													style={st === SLA.UNKNOWN ? undefined : `background: ${st.color};`}
													title="{r.team} · {matrixServices[i]?.name ?? ''} · {st.label}{cell.latency_ms != null ? ` · ${cell.latency_ms}ms` : ''}"
												></span>
											</td>
										{/each}
										<td class="px-3 sm:px-4 py-1 text-right text-xs tabular-nums border-t border-stone-800/60 {up < r.cells.length ? 'text-stone-300' : 'text-stone-500'}">
											{up}/{r.cells.length}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
					<div class="px-4 py-3 border-t border-stone-800 space-y-2.5 text-[0.7rem] leading-none text-stone-500">
						<div class="flex flex-wrap gap-x-4 gap-y-2">
							{#each matrixServices as s, i (s.service_id)}
								<span class="inline-flex items-center gap-1.5">
									<span class="tabular-nums font-semibold text-stone-300">{i + 1}</span><span class="optical-label">{s.name}</span>
								</span>
							{/each}
						</div>
						<div class="flex flex-wrap gap-x-4 gap-y-2">
							{#each LEGEND as s}
								<span class="inline-flex items-center gap-1.5">
									<span class="w-2.5 h-2.5 rounded-[2px]" style="background: {SLA[s].color};"></span><span class="optical-label">{SLA[s].label}</span>
								</span>
							{/each}
							{#if hasUnknown}
								<span class="inline-flex items-center gap-1.5">
									<span class="w-2.5 h-2.5 rounded-[2px] bg-stone-800"></span><span class="optical-label">{SLA.UNKNOWN.label}</span>
								</span>
							{/if}
						</div>
					</div>
				{/if}
			</div>

			<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden flex flex-col">
				<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
					<h2 class="text-[0.95rem] font-semibold text-stone-200">Captures</h2>
					<span class="text-stone-600 text-xs ml-auto tabular-nums">{events.length}</span>
				</div>
				<!-- desktop: the list fills the row the status card sets and scrolls inside it -->
				<div class="relative lg:flex-1 lg:min-h-[12rem]">
					<div class="p-1.5 overflow-y-auto max-h-[21rem] lg:max-h-none lg:absolute lg:inset-0">
						{#if events.length === 0}
							<div class="px-4 py-8 text-center text-stone-500 text-sm">No captures yet.</div>
						{:else}
							<ul>
								{#each events as e (eventKey(e))}
									{@const ac = teamColor(e.attacker)}
									<li
										class="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm leading-none transition-colors {flashEvents.has(eventKey(e))
											? 'bg-amber-500/5'
											: ''}"
									>
										<div class="min-w-0 flex-1 truncate" title="tick {e.tick}">
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
		</div>
		{/if}

		<ArenaChart {history} {kothHistory} leader={standings[0]?.team ?? null} />

		<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden">
			<div class="px-4 py-3 border-b border-stone-800">
				<h2 class="text-[0.95rem] font-semibold text-stone-200">Standings</h2>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full text-sm {showBreakdown ? 'min-w-[720px]' : 'min-w-[24rem]'}">
					<thead>
						<tr class="metadata-label text-stone-500">
							<th class="px-4 py-2.5 text-left w-12">#</th>
							<th class="px-4 py-2.5 text-left">Team</th>
							{#if !showBreakdown}
							<th class="px-4 py-2.5 text-right text-stone-200">Total</th>
							{:else if weights}
							<th class="px-4 py-2.5 text-right text-stone-200">Score</th>
							{#each parts as part (part.key)}
							<th class="px-4 py-2.5 text-right whitespace-nowrap">{part.label}</th>
							{/each}
							{:else}
							{#if hasAD}
							<th class="px-4 py-2.5 text-left hidden lg:table-cell w-36">Breakdown</th>
							<th class="px-4 py-2.5 text-right">Attack</th>
							<th class="px-4 py-2.5 text-right">Defense</th>
							<th class="px-4 py-2.5 text-right">Uptime</th>
							{/if}
							<th class="px-4 py-2.5 text-right">KotH</th>
							<th class="px-4 py-2.5 text-right">Total</th>
							{/if}
						</tr>
					</thead>
					<tbody>
						{#each standings as team (team.team_id)}
							{@const c = teamColor(team.team_id || team.team)}
							{@const sum = Math.max(1, team.attack + team.defense + team.sla + team.koth)}
							<tr class="border-t border-stone-800/60 hover:bg-stone-800/20 transition-colors">
								<td class="px-4 py-2.5 whitespace-nowrap text-stone-300 font-semibold tabular-nums">{team.rank ?? '-'}</td>
								<td class="px-4 py-2.5 whitespace-nowrap">
									<div class="flex items-center gap-2.5 leading-none">
										<span class="w-2 h-2 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="optical-label text-stone-200 truncate max-w-[220px]">{team.team}</span>
									</div>
								</td>
								{#if !showBreakdown}
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-amber-500/90 font-semibold tabular-nums">{pts(team.total)}</td>
								{:else if weights}
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-amber-500/90 font-semibold tabular-nums">{pts(team.total)}</td>
								{#each parts as part (part.key)}
								<td
									class="px-4 py-2.5 whitespace-nowrap text-right tabular-nums"
									title={part.key === 'ad' ? `Attack ${pts(team.attack)}, defense ${pts(team.defense)}, uptime ${pts(team.sla)}` : undefined}
								>
									<span class="text-stone-300">{signed(team.share?.[part.key])}</span>
									<span class="block text-[11px] text-stone-500">{part.short} {pts(raw(team, part.key))} pts</span>
								</td>
								{/each}
								{:else}
								{#if hasAD}
								<td class="px-4 py-2.5 hidden lg:table-cell">
									<div class="flex h-1.5 w-32 overflow-hidden rounded-full bg-stone-800/80">
										<div style="width: {(team.attack / sum) * 100}%; background: {bd.attack};" title="Attack {fmt(team.attack)}"></div>
										<div style="width: {(team.defense / sum) * 100}%; background: {bd.defense};" title="Defense {fmt(team.defense)}"></div>
										<div style="width: {(team.sla / sum) * 100}%; background: {bd.sla};" title="Uptime {fmt(team.sla)}"></div>
										<div style="width: {(team.koth / sum) * 100}%; background: {bd.koth};" title="KotH {fmt(team.koth)}"></div>
									</div>
								</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.attack)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right tabular-nums {team.defense < 0 ? 'text-red-400/80' : 'text-stone-400'}">{fmt(team.defense)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.sla)}</td>
								{/if}
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums">{fmt(team.koth)}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-amber-500/90 font-semibold tabular-nums">{fmt(team.total)}</td>
								{/if}
							</tr>
						{/each}
						{#if standings.length === 0}
							<tr><td colspan={standingsColumns} class="px-4 py-8 text-center text-stone-500">No standings yet.</td></tr>
						{/if}
					</tbody>
				</table>
			</div>
		</div>
		{/if}
	{/if}
</div>

<style>
	.fade-in {
		animation: fadeIn 0.4s ease-out;
	}
	/* sticky cells: the card's own colour, opaque, so scrolled tiles don't show through */
	.solid-cell {
		background: linear-gradient(rgb(var(--st-900) / 0.4), rgb(var(--st-900) / 0.4)), rgb(var(--st-950));
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
	@media (prefers-reduced-motion: reduce) {
		.fade-in {
			animation: none;
		}
	}
</style>
