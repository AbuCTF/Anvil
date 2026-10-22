<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { API_BASE } from '$lib/config';

	type ServiceStatus = 'up' | 'mumble' | 'down';

	interface WarTeam {
		id: string;
		name: string;
		is_nop: boolean;
	}

	interface WarService {
		slug: string;
		name: string;
		port: number;
	}

	interface GridCell {
		team_id: string;
		service_slug: string;
		status: ServiceStatus;
		breach_flash: boolean;
		is_own: boolean;
	}

	interface HillState {
		slug: string;
		name: string;
		holder_name: string | null;
		held_ticks: number;
		round_ends_in: number;
		is_active: boolean;
	}

	interface EventEntry {
		kind: string;
		team_name?: string;
		service?: string;
		hill?: string;
		text?: string;
		tick: number;
		ts: string;
	}

	interface WarRoomState {
		tick: number;
		view_tick: number;
		frozen: boolean;
		teams: WarTeam[];
		services: WarService[];
		grid: GridCell[];
		hills: HillState[];
		events: EventEntry[];
	}

	interface EventMeta {
		icon: string;
		className: string;
		label: string;
	}

	const POLL_MS = 5000;
	const IDLE_POLL_MS = 30000;
	const REQUEST_TIMEOUT_MS = 10000;

	const statusMeta: Record<ServiceStatus, { color: string; label: string }> = {
		up: { color: '#4b7355', label: 'Up' },
		mumble: { color: '#9c7a30', label: 'Degraded' },
		down: { color: '#b0453a', label: 'Down' },
	};

	let state: WarRoomState | null = null;
	let now = Date.now();
	let snapshotAt = Date.now();
	let loading = true;
	let unavailable = false;
	let error = '';
	let refreshError = '';
	let inFlight = false;
	let disposed = false;
	let timer: ReturnType<typeof setTimeout>;
	let requestController: AbortController | null = null;

	$: cellMap = new Map((state?.grid ?? []).map((cell) => [`${cell.team_id}:${cell.service_slug}`, cell]));
	$: delayedTicks = state ? Math.max(0, state.tick - state.view_tick) : 0;
	$: ownTeamID = state?.grid.find((cell) => cell.is_own)?.team_id ?? '';
	$: serviceCounts = (state?.grid ?? []).reduce(
		(counts, cell) => {
			counts[cell.status]++;
			return counts;
		},
		{ up: 0, mumble: 0, down: 0 } as Record<ServiceStatus, number>,
	);

	function authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
		return token ? { Authorization: `Bearer ${token}` } : {};
	}

	function schedule() {
		clearTimeout(timer);
		if (disposed || document.visibilityState !== 'visible') return;
		timer = setTimeout(load, state ? POLL_MS : IDLE_POLL_MS);
	}

	async function load() {
		if (inFlight || disposed) return;
		inFlight = true;
		const controller = new AbortController();
		requestController = controller;
		let timedOut = false;
		const requestTimeout = setTimeout(() => {
			timedOut = true;
			controller.abort();
		}, REQUEST_TIMEOUT_MS);

		try {
			const response = await fetch(`${API_BASE}/api/v1/arena/war-room`, {
				headers: authHeaders(),
				signal: controller.signal,
			});
			if (response.status === 404) {
				state = null;
				unavailable = true;
				error = '';
				refreshError = '';
				return;
			}
			if (!response.ok) throw new Error(`War Room unavailable (HTTP ${response.status})`);

			state = (await response.json()) as WarRoomState;
			snapshotAt = Date.now();
			unavailable = false;
			error = '';
			refreshError = response.headers.get('X-Anvil-Cache') === 'STALE' ? 'Live updates are delayed' : '';
		} catch (cause) {
			if (disposed || (controller.signal.aborted && !timedOut)) return;
			const message = timedOut
				? 'War Room request timed out'
				: cause instanceof Error
					? cause.message
					: 'War Room unavailable';
			if (state) refreshError = message;
			else error = message;
		} finally {
			clearTimeout(requestTimeout);
			if (requestController === controller) requestController = null;
			loading = false;
			inFlight = false;
			schedule();
		}
	}

	function cellFor(teamID: string, serviceSlug: string): GridCell | undefined {
		return cellMap.get(`${teamID}:${serviceSlug}`);
	}

	function eventMeta(kind: string): EventMeta {
		switch (kind) {
			case 'first_blood':
				return {
					icon: 'mdi:flag-checkered',
					className: 'text-[#e0483c]',
					label: 'First blood',
				};
			case 'shield_up':
				return {
					icon: 'mdi:shield-check',
					className: 'text-[#78a584]',
					label: 'Shield up',
				};
			case 'crown':
				return {
					icon: 'mdi:crown',
					className: 'text-amber-500/80',
					label: 'Crown',
				};
			case 'hill_open':
				return {
					icon: 'mdi:gate-open',
					className: 'text-stone-400',
					label: 'Hill opened',
				};
			case 'hill_close':
				return {
					icon: 'mdi:gate',
					className: 'text-stone-500',
					label: 'Hill closed',
				};
			case 'advisory':
				return {
					icon: 'mdi:information-outline',
					className: 'text-[#6f91a8]',
					label: 'Advisory',
				};
			default:
				return {
					icon: 'mdi:bell-outline',
					className: 'text-stone-500',
					label: 'Event',
				};
		}
	}

	function eventSummary(event: EventEntry): string {
		if (event.text) return event.text;
		return [event.team_name, event.service, event.hill].filter(Boolean).join(' / ');
	}

	function eventKey(event: EventEntry): string {
		return `${event.kind}:${event.tick}:${event.ts}:${event.team_name ?? ''}:${event.service ?? ''}:${event.hill ?? ''}`;
	}

	function eventAge(timestamp: string): string {
		const parsed = Date.parse(timestamp);
		if (!Number.isFinite(parsed)) return '-';
		const seconds = Math.max(0, Math.floor((now - parsed) / 1000));
		if (seconds < 60) return `${seconds}s`;
		if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
		return `${Math.floor(seconds / 3600)}h`;
	}

	function hillSeconds(hill: HillState): number {
		return Math.max(0, hill.round_ends_in - Math.floor((now - snapshotAt) / 1000));
	}

	function formatTimer(seconds: number): string {
		return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`;
	}

	onMount(() => {
		disposed = false;
		const clock = setInterval(() => (now = Date.now()), 1000);
		const onVisibilityChange = () => {
			clearTimeout(timer);
			requestController?.abort();
			if (document.visibilityState === 'visible') load();
		};

		load();
		document.addEventListener('visibilitychange', onVisibilityChange);
		return () => {
			disposed = true;
			clearInterval(clock);
			clearTimeout(timer);
			requestController?.abort();
			document.removeEventListener('visibilitychange', onVisibilityChange);
		};
	});
</script>

<svelte:head>
	<title>War Room - Anvil</title>
</svelte:head>

<div class="w-full px-4 py-6 sm:px-6 lg:px-8 2xl:px-10">
	{#if loading}
		<div class="space-y-4" aria-label="Loading War Room">
			<div class="h-9 w-64 animate-pulse rounded-md bg-stone-900/60"></div>
			<div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
				<div class="h-96 animate-pulse rounded-lg border border-stone-800 bg-stone-900/40"></div>
				<div class="h-96 animate-pulse rounded-lg border border-stone-800 bg-stone-900/40"></div>
			</div>
		</div>
	{:else if error}
		<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-6 text-center">
			<Icon icon="mdi:alert-circle-outline" class="mx-auto mb-3 h-10 w-10 text-stone-500" />
			<p class="text-sm text-stone-400">{error}</p>
			<p class="mt-1 text-xs text-stone-600">
				Retrying every {IDLE_POLL_MS / 1000}s…
			</p>
		</div>
	{:else if unavailable || !state}
		<PageHeader title="War Room" compact>
			<a
				slot="actions"
				href="/arena"
				class="inline-flex items-center gap-1.5 text-xs text-stone-500 transition-colors hover:text-stone-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-500/60"
			>
				<OpticalIcon icon="mdi:arrow-left" size={13} box={13} />
				<span class="optical-label">Arena overview</span>
			</a>
		</PageHeader>
		<EmptyState icon="mdi:shield-off-outline" text="War Room is not available for this event." />
	{:else}
		<PageHeader title="War Room" compact>
			<div slot="actions" class="flex flex-wrap items-center justify-end gap-2">
				<a
					href="/arena"
					class="inline-flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-stone-500 transition-colors hover:bg-stone-800/30 hover:text-stone-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-500/60"
				>
					<OpticalIcon icon="mdi:arrow-left" size={13} box={13} />
					<span class="optical-label">Arena overview</span>
				</a>
				<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5 text-xs">
					<span class="metadata-label text-stone-500">Tick</span>
					<span class="optical-label font-semibold tabular-nums text-stone-100">{state.tick}</span>
				</div>
				<span
					class="inline-flex items-center gap-1.5 text-xs {refreshError
						? 'text-[#9c7a30]'
						: state.frozen
							? 'text-stone-400'
							: 'text-stone-600'}"
					title={refreshError || (state.frozen ? 'Public view frozen' : 'Live War Room feed')}
				>
					<span
						class="h-1.5 w-1.5 rounded-full {refreshError
							? 'bg-[#9c7a30]'
							: state.frozen
								? 'bg-stone-500'
								: 'animate-pulse bg-amber-500/70'}"
					></span>
					<span class="optical-label">{refreshError ? 'delayed' : state.frozen ? 'frozen' : 'live'}</span>
				</span>
			</div>
		</PageHeader>

		<div
			class="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-md border border-stone-800 bg-stone-900/30 px-3 py-2 text-xs text-stone-500"
		>
			<span>Public view <span class="tabular-nums text-stone-300">t{state.view_tick}</span></span>
			<span>{delayedTicks ? `${delayedTicks}-tick disclosure delay` : 'current public tick'}</span>
			<span class="text-stone-600">Breach indicators never reveal the attacker.</span>
		</div>

		<div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
			<section
				class="overflow-hidden rounded-lg border border-stone-800 bg-stone-900/40"
				aria-labelledby="service-grid-title"
			>
				<div class="flex flex-wrap items-center gap-3 border-b border-stone-800 px-4 py-3">
					<h2 id="service-grid-title" class="text-[0.95rem] font-semibold text-stone-200">Service status</h2>
					<span class="ml-auto text-xs text-stone-600"
						><span class="tabular-nums">{state.teams.length}</span> teams ·
						<span class="tabular-nums">{state.services.length}</span> services</span
					>
				</div>

				{#if state.services.length === 0 || state.teams.length === 0}
					<div class="px-4 py-12 text-center text-sm text-stone-500">No released services yet.</div>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full min-w-[660px] border-separate border-spacing-0 text-xs">
							<thead>
								<tr>
									<th
										scope="col"
										class="metadata-label sticky left-0 z-20 w-44 border-b border-stone-800 bg-stone-950 px-4 py-2.5 text-left text-stone-500"
										>Team</th
									>
									{#each state.services as service (service.slug)}
										<th
											scope="col"
											class="min-w-24 border-b border-stone-800 px-2 py-2 text-center font-normal text-stone-400"
											title={`${service.name}${service.port ? ` on port ${service.port}` : ''}`}
										>
											<span class="block truncate">{service.name}</span>
											{#if service.port}<span class="mt-0.5 block tabular-nums text-[0.62rem] text-stone-700"
													>:{service.port}</span
												>{/if}
										</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each state.teams as team (team.id)}
									<tr class={team.id === ownTeamID ? 'bg-amber-500/[0.025]' : ''}>
										<th
											scope="row"
											class="sticky left-0 z-10 border-b border-stone-800/60 bg-stone-950 px-4 py-2 text-left font-normal"
										>
											<div class="flex min-w-0 items-center gap-2">
												{#if team.id === ownTeamID}<span
														class="h-1.5 w-1.5 shrink-0 rounded-full bg-amber-500/70"
														title="Your team"
													></span>{/if}
												<span class="truncate text-stone-300" title={team.name}>{team.name}</span>
												{#if team.is_nop}<span
														class="badge-label ml-auto rounded border border-stone-800 px-1.5 py-0.5 text-[0.58rem] text-stone-600"
														>reference</span
													>{/if}
											</div>
										</th>
										{#each state.services as service (service.slug)}
											{@const cell = cellFor(team.id, service.slug)}
											{@const status = cell?.status ?? 'down'}
											<td class="border-b border-stone-800/60 px-2 py-1.5 text-center">
												<span
													class="relative mx-auto flex h-7 w-7 items-center justify-center rounded-sm border border-white/5 {cell?.breach_flash
														? 'breach-flash ring-1 ring-[#e0483c]/70'
														: ''} {cell?.is_own ? 'ring-1 ring-amber-500/50' : ''}"
													style={`background: ${statusMeta[status].color};`}
													role="img"
													aria-label={`${team.name}, ${service.name}: ${statusMeta[status].label}${cell?.breach_flash ? ', breach detected' : ''}${cell?.is_own ? ', your service' : ''}`}
													title={`${team.name} · ${service.name} · ${statusMeta[status].label}${cell?.breach_flash ? ' · breach detected' : ''}`}
												>
													{#if cell?.breach_flash}<Icon icon="mdi:alert" class="h-3.5 w-3.5 text-white/90" />{/if}
												</span>
											</td>
										{/each}
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}

				<div class="flex flex-wrap gap-x-4 gap-y-2 border-t border-stone-800 px-4 py-2.5">
					{#each Object.entries(statusMeta) as [key, meta]}
						<span class="inline-flex items-center gap-1.5 text-[0.7rem] text-stone-500">
							<span class="h-2.5 w-2.5 rounded-sm" style={`background: ${meta.color};`}></span>
							<span class="optical-label">{meta.label}</span>
							<span class="tabular-nums text-stone-700">{serviceCounts[key as ServiceStatus]}</span>
						</span>
					{/each}
					<span class="inline-flex items-center gap-1.5 text-[0.7rem] text-stone-500">
						<span class="flex h-2.5 w-2.5 items-center justify-center rounded-sm bg-[#b0453a] ring-1 ring-[#e0483c]/70"
						></span>
						<span class="optical-label">Breach detected</span>
					</span>
				</div>
			</section>

			<div class="flex min-w-0 flex-col gap-4">
				<section
					class="overflow-hidden rounded-lg border border-stone-800 bg-stone-900/40"
					aria-labelledby="hills-title"
				>
					<div class="flex items-center gap-2 border-b border-stone-800 px-4 py-3">
						<OpticalIcon icon="mdi:crown-outline" size={14} box={14} className="text-stone-500" />
						<h2 id="hills-title" class="text-[0.95rem] font-semibold text-stone-200">Hill control</h2>
						<span class="ml-auto tabular-nums text-xs text-stone-600">{state.hills.length}</span>
					</div>
					{#if state.hills.length === 0}
						<div class="px-4 py-8 text-center text-sm text-stone-500">No hills scheduled.</div>
					{:else}
						<div class="divide-y divide-stone-800/60">
							{#each state.hills as hill (hill.slug)}
								<div class="px-4 py-3">
									<div class="mb-2 flex items-center gap-2">
										<span class="min-w-0 flex-1 truncate text-xs font-medium text-stone-300" title={hill.name}
											>{hill.name}</span
										>
										{#if hill.is_active && hill.round_ends_in > 0}
											<span class="tabular-nums text-[0.65rem] text-stone-600" title="Round time remaining"
												>{formatTimer(hillSeconds(hill))}</span
											>
										{:else if !hill.is_active}
											<span
												class="badge-label rounded border border-stone-800 px-1.5 py-0.5 text-[0.58rem] text-stone-600"
												>scheduled</span
											>
										{/if}
									</div>
									{#if hill.is_active}
										<div class="flex items-center gap-2 text-xs">
											<Icon
												icon={hill.holder_name ? 'mdi:crown' : 'mdi:crown-outline'}
												class="h-3.5 w-3.5 shrink-0 {hill.holder_name ? 'text-amber-500/80' : 'text-stone-700'}"
											/>
											<span class="min-w-0 flex-1 truncate {hill.holder_name ? 'text-stone-300' : 'text-stone-600'}"
												>{hill.holder_name ?? 'Open'}</span
											>
											{#if hill.holder_name}<span class="tabular-nums text-stone-600">{hill.held_ticks}t</span>{/if}
										</div>
									{:else}
										<p class="text-xs text-stone-600">Not currently active</p>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				</section>

				<section
					class="min-h-0 overflow-hidden rounded-lg border border-stone-800 bg-stone-900/40"
					aria-labelledby="events-title"
				>
					<div class="flex items-center gap-2 border-b border-stone-800 px-4 py-3">
						<OpticalIcon icon="mdi:bell-outline" size={14} box={14} className="text-stone-500" />
						<h2 id="events-title" class="text-[0.95rem] font-semibold text-stone-200">Event feed</h2>
						<span class="ml-auto tabular-nums text-xs text-stone-600">{state.events.length}</span>
					</div>
					{#if state.events.length === 0}
						<div class="px-4 py-8 text-center text-sm text-stone-500">No public events yet.</div>
					{:else}
						<ul class="max-h-[26rem] divide-y divide-stone-800/40 overflow-y-auto">
							{#each state.events as event (eventKey(event))}
								{@const meta = eventMeta(event.kind)}
								<li class="flex items-start gap-2.5 px-4 py-2.5 text-xs">
									<Icon icon={meta.icon} class="mt-0.5 h-3.5 w-3.5 shrink-0 {meta.className}" />
									<div class="min-w-0 flex-1">
										<div class="flex items-baseline gap-2">
											<span class="font-medium {meta.className}">{meta.label}</span>
											<span class="tabular-nums text-[0.62rem] text-stone-700">t{event.tick}</span>
										</div>
										{#if eventSummary(event)}<p class="mt-0.5 break-words leading-relaxed text-stone-400">
												{eventSummary(event)}
											</p>{/if}
									</div>
									<span class="shrink-0 tabular-nums text-[0.65rem] text-stone-700">{eventAge(event.ts)}</span>
								</li>
							{/each}
						</ul>
					{/if}
				</section>
			</div>
		</div>
	{/if}
</div>

<style>
	.breach-flash {
		animation: breach-pulse 1.4s ease-in-out infinite;
	}

	@keyframes breach-pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.62;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.breach-flash,
		.animate-pulse {
			animation: none;
		}
	}
</style>
