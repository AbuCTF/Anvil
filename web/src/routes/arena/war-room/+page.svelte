<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { platformInfo } from '$lib/stores/platform';
	import { API_BASE } from '$lib/config';

	// ---- types ---------------------------------------------------------------
	type SvcStatus = 'up' | 'mumble' | 'down';
	interface GridCell {
		team_id: string;
		service_slug: string;
		status: SvcStatus;
		breach_flash: boolean; // victim saw breach in last 60s (delayed, no attacker name)
		is_own: boolean;
	}
	interface HillState {
		slug: string;
		name: string;
		holder_name: string | null;
		held_ticks: number;
		round_ends_in: number; // seconds
		is_active: boolean;
	}
	// /api/v1/arena/koth/scores shape (P1-confirmed, v0.344.0)
	interface KothEntry {
		hill_id: string;
		hill: string;
		team_id: string | null;
		team_name: string | null;
		held_ticks: number;
		score: number;
	}
	interface EventEntry {
		kind: 'first_blood' | 'shield_up' | 'crown' | 'service_down' | 'koth_flip';
		team_name: string;
		service?: string;
		hill?: string;
		tick: number;
		ts: string;
	}
	interface WarRoomState {
		tick: number;
		teams: { id: string; name: string }[];
		services: { slug: string; name: string; port: number }[];
		grid: GridCell[];
		hills: HillState[];
		events: EventEntry[];
	}

	// ---- mock data -----------------------------------------------------------
	const TEAMS = [
		'ByteBrigade','CipherPunk','RedSignal','NullRoute','PacketStorm',
		'SegFault','DeepProbe','VoidMatrix','IronSocket','GhostBit',
		'LoopBreak','NetMancer','HexWarden','BinVector','DarkShell'
	];
	const SERVICES = [
		{ slug: 'harbor-id', name: 'Harbor ID', port: 8001 },
		{ slug: 'tidepay', name: 'TidePay', port: 8002 },
		{ slug: 'bollard', name: 'Bollard', port: 8003 },
		{ slug: 'berth-board', name: 'Berth Board', port: 8004 },
		{ slug: 'dockyard', name: 'Dockyard', port: 8005 },
		{ slug: 'tariff-studio', name: 'Tariff Studio', port: 8006 },
	];

	function mkGrid(): GridCell[] {
		const cells: GridCell[] = [];
		const statusPool: SvcStatus[] = ['up','up','up','up','up','up','up','up','mumble','down'];
		for (let ti = 0; ti < TEAMS.length; ti++) {
			for (let si = 0; si < SERVICES.length; si++) {
				cells.push({
					team_id: `t${ti}`,
					service_slug: SERVICES[si].slug,
					status: statusPool[(ti * 3 + si * 7) % statusPool.length],
					breach_flash: Math.random() < 0.08,
					is_own: ti === 0
				});
			}
		}
		return cells;
	}

	const MOCK_STATE: WarRoomState = {
		tick: 87,
		teams: TEAMS.map((name, i) => ({ id: `t${i}`, name })),
		services: SERVICES,
		grid: mkGrid(),
		hills: [
			{ slug: 'gridwatch-ii', name: 'GridWatch II', holder_name: 'ByteBrigade', held_ticks: 22, round_ends_in: 47, is_active: true },
			{ slug: 'hostile-takeover-ii', name: 'Hostile Takeover II', holder_name: 'CipherPunk', held_ticks: 17, round_ends_in: 0, is_active: true },
			{ slug: 'harbor-master', name: 'Harbor Master', holder_name: null, held_ticks: 0, round_ends_in: 0, is_active: false },
		],
		events: [
			{ kind: 'crown', team_name: 'ByteBrigade', hill: 'GridWatch II', tick: 86, ts: new Date(Date.now() - 120000).toISOString() },
			{ kind: 'shield_up', team_name: 'NullRoute', service: 'TidePay', tick: 85, ts: new Date(Date.now() - 240000).toISOString() },
			{ kind: 'first_blood', team_name: 'SegFault', service: 'Harbor ID', tick: 84, ts: new Date(Date.now() - 360000).toISOString() },
			{ kind: 'koth_flip', team_name: 'CipherPunk', hill: 'Hostile Takeover II', tick: 83, ts: new Date(Date.now() - 480000).toISOString() },
			{ kind: 'service_down', team_name: 'DarkShell', service: 'Bollard', tick: 82, ts: new Date(Date.now() - 600000).toISOString() },
		]
	};

	// ---- state ---------------------------------------------------------------
	let state: WarRoomState = MOCK_STATE;
	let kothEntries: KothEntry[] = []; // wired: /arena/koth/scores
	let now = Date.now();
	let clockInterval: ReturnType<typeof setInterval>;
	let pollInterval: ReturnType<typeof setInterval>;

	$: isFinalsMode = !!$platformInfo?.game_state;

	// cell lookup
	$: cellMap = new Map(state.grid.map(c => [`${c.team_id}:${c.service_slug}`, c]));

	// prefer live KotH entries if loaded; fall back to mock hills
	$: hills = kothEntries.length
		? kothEntries.map(e => ({
			slug: e.hill_id,
			name: e.hill,
			holder_name: e.team_name,
			held_ticks: e.held_ticks,
			round_ends_in: 0,
			is_active: true,
		}))
		: state.hills;

	function authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
		return token ? { Authorization: `Bearer ${token}` } : {};
	}

	async function poll() {
		if (!isFinalsMode) return;
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/war-room`, { headers: authHeaders() });
			if (res.ok) state = await res.json();
		} catch { /* keep mock */ }
	}

	async function loadKothScores() {
		if (!isFinalsMode) return;
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/koth/scores`, { headers: authHeaders() });
			if (res.ok) { const d = await res.json(); kothEntries = d.hills ?? []; }
		} catch { /* keep current */ }
	}

	function cellStatus(teamId: string, svcSlug: string): GridCell | undefined {
		return cellMap.get(`${teamId}:${svcSlug}`);
	}

	const statusColor: Record<SvcStatus, string> = {
		up: 'bg-[#4b7355]',
		mumble: 'bg-[#9c7a30]',
		down: 'bg-[#b0453a]'
	};
	const statusBorder: Record<SvcStatus, string> = {
		up: 'border-[#4b7355]/60',
		mumble: 'border-[#9c7a30]/60',
		down: 'border-[#b0453a]/60'
	};

	const eventIcon: Record<EventEntry['kind'], { icon: string; cls: string; label: string }> = {
		first_blood: { icon: 'mdi:flag-checkered', cls: 'text-[#e08a85]', label: 'FIRST BLOOD' },
		shield_up: { icon: 'mdi:shield-check', cls: 'text-[#9bcea8]', label: 'SHIELD UP' },
		crown: { icon: 'mdi:crown', cls: 'text-amber-400', label: 'CROWN' },
		service_down: { icon: 'mdi:server-off', cls: 'text-[#b0453a]', label: 'DOWN' },
		koth_flip: { icon: 'mdi:swap-horizontal', cls: 'text-stone-400', label: 'HILL FLIP' }
	};

	function eventAge(ts: string): string {
		const s = Math.round((now - Date.parse(ts)) / 1000);
		if (s < 60) return `${s}s`;
		if (s < 3600) return `${Math.floor(s / 60)}m`;
		return `${Math.floor(s / 3600)}h`;
	}

	function fmtHillTimer(secs: number): string {
		if (secs <= 0) return '--:--';
		return `${String(Math.floor(secs / 60)).padStart(2,'0')}:${String(secs % 60).padStart(2,'0')}`;
	}

	onMount(() => {
		clockInterval = setInterval(() => { now = Date.now(); }, 1000);
		pollInterval = setInterval(() => { poll(); loadKothScores(); }, 15000);
		loadKothScores();
	});
	onDestroy(() => {
		clearInterval(clockInterval);
		clearInterval(pollInterval);
	});
</script>

<svelte:head><title>War Room - Anvil</title></svelte:head>

{#if !isFinalsMode}
	<div class="flex items-center justify-center py-24 text-stone-600 text-sm">War Room is only available during the finals.</div>
{:else}
<div class="w-full px-4 sm:px-6 lg:px-8 py-6 min-h-screen bg-stone-950">
	<!-- header bar -->
	<div class="mb-4 flex flex-wrap items-center gap-3">
		<h1 class="text-lg font-semibold text-stone-100">War Room</h1>
		<span class="rounded border border-stone-800 bg-stone-900/60 px-2.5 py-0.5 text-xs tabular-nums text-stone-400">Tick {state.tick}</span>
		<span class="text-xs text-stone-600">Delayed 1 tick. Attacker identities hidden during game.</span>
		<a href="/arena" class="ml-auto text-xs text-stone-600 hover:text-stone-400 transition-colors">Arena</a>
		<a href="/scoreboard" class="text-xs text-stone-600 hover:text-stone-400 transition-colors">Scoreboard</a>
	</div>

	<div class="grid gap-4 lg:grid-cols-[1fr_20rem]">
		<!-- left: service status grid -->
		<div>
			<!-- service header row -->
			<div class="mb-1 overflow-x-auto">
				<table class="w-full text-xs">
					<thead>
						<tr class="text-stone-600">
							<th class="w-28 shrink-0 py-1 pr-2 text-left">Team</th>
							{#each state.services as svc}
								<th class="px-1 py-1 text-center font-normal truncate max-w-[3.5rem]" title="{svc.name} :{svc.port}">{svc.name}</th>
							{/each}
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-900">
						{#each state.teams as team}
							{@const cells = state.services.map(s => cellStatus(team.id, s.slug))}
							<tr class="group">
								<td class="truncate py-1 pr-2 text-xs text-stone-400 max-w-[7rem]" title={team.name}>{team.name}</td>
								{#each cells as cell, si}
									{@const s = cell?.status ?? 'down'}
									<td class="px-1 py-0.5">
										<div
											class="relative h-5 w-6 mx-auto rounded-sm border {statusColor[s]} {statusBorder[s]} {cell?.breach_flash ? 'animate-pulse ring-1 ring-red-400/60' : ''} {cell?.is_own ? 'ring-1 ring-amber-400/40' : ''}"
											title="{team.name} / {state.services[si]?.name ?? ''}: {s}"
										>
											{#if cell?.breach_flash}
												<span class="absolute inset-0 flex items-center justify-center text-[0.4rem] font-bold text-white/80">!</span>
											{/if}
										</div>
									</td>
								{/each}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<!-- legend -->
			<div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[0.65rem] text-stone-600">
				<span class="inline-flex items-center gap-1"><span class="h-2 w-3 rounded-sm bg-[#4b7355]"></span>up</span>
				<span class="inline-flex items-center gap-1"><span class="h-2 w-3 rounded-sm bg-[#9c7a30]"></span>mumble</span>
				<span class="inline-flex items-center gap-1"><span class="h-2 w-3 rounded-sm bg-[#b0453a]"></span>down</span>
				<span class="inline-flex items-center gap-1"><span class="h-2 w-3 rounded-sm bg-[#b0453a] ring-1 ring-red-400/60"></span>breach detected</span>
				<span class="inline-flex items-center gap-1"><span class="h-2 w-3 rounded-sm bg-[#4b7355] ring-1 ring-amber-400/40"></span>your service</span>
			</div>
		</div>

		<!-- right: hills + event ticker -->
		<div class="flex flex-col gap-4">
			<!-- hills panel -->
			<div class="rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden">
				<div class="border-b border-stone-800 px-4 py-2.5 flex items-center gap-2">
					<Icon icon="mdi:crown" class="h-4 w-4 text-amber-400" />
					<span class="text-sm font-medium text-stone-300">Hills</span>
				</div>
				<div class="divide-y divide-stone-800/60">
					{#each hills as hill}
						<div class="px-4 py-3">
							<div class="flex items-center gap-2 mb-1">
								<span class="text-xs font-medium text-stone-200 truncate flex-1">{hill.name}</span>
								{#if !hill.is_active}
									<span class="text-[0.6rem] rounded bg-stone-800 px-1.5 py-0.5 text-stone-600">not released</span>
								{:else if hill.holder_name}
									<span class="text-[0.6rem] font-mono tabular-nums text-stone-500">{fmtHillTimer(hill.round_ends_in - Math.round((now - Date.now()) / 1000))}</span>
								{/if}
							</div>
							{#if hill.is_active}
								<div class="flex items-center gap-2 text-xs">
									{#if hill.holder_name}
										<Icon icon="mdi:crown" class="h-3 w-3 text-amber-400 shrink-0" />
										<span class="font-medium text-stone-200 truncate">{hill.holder_name}</span>
										<span class="tabular-nums text-stone-600 ml-auto">{hill.held_ticks}t</span>
									{:else}
										<span class="text-stone-600 italic">contested</span>
									{/if}
								</div>
								<!-- hold bar -->
								<div class="mt-1.5 h-1 w-full rounded-full bg-stone-800 overflow-hidden">
									<div
										class="h-full rounded-full bg-amber-400/60 transition-all"
										style="width: {Math.min(100, (hill.held_ticks / 50) * 100)}%"
									></div>
								</div>
							{/if}
						</div>
					{/each}
				</div>
			</div>

			<!-- event ticker -->
			<div class="rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden flex-1">
				<div class="border-b border-stone-800 px-4 py-2.5 flex items-center gap-2">
					<Icon icon="mdi:bell-outline" class="h-4 w-4 text-stone-500" />
					<span class="text-sm font-medium text-stone-300">Events</span>
				</div>
				<ul class="divide-y divide-stone-800/40 overflow-y-auto max-h-64">
					{#each state.events as ev}
						{@const meta = eventIcon[ev.kind]}
						<li class="flex items-start gap-2.5 px-4 py-2.5 text-xs">
							<Icon icon={meta.icon} class="h-3.5 w-3.5 shrink-0 mt-0.5 {meta.cls}" />
							<div class="flex-1 min-w-0">
								<span class="font-medium {meta.cls}">{meta.label}</span>
								<span class="text-stone-400 ml-1">{ev.team_name}</span>
								{#if ev.service}<span class="text-stone-600 ml-1">/ {ev.service}</span>{/if}
								{#if ev.hill}<span class="text-stone-600 ml-1">/ {ev.hill}</span>{/if}
							</div>
							<span class="text-stone-700 tabular-nums shrink-0">{eventAge(ev.ts)}</span>
						</li>
					{/each}
				</ul>
			</div>
		</div>
	</div>
</div>
{/if}
