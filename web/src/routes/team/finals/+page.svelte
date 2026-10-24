<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { platformInfo } from '$lib/stores/platform';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import HelpTip from '$lib/components/HelpTip.svelte';
	import CopyText from '$lib/components/CopyText.svelte';
	import {
		PUBLIC_ACCESS, PUBLIC_HOST, SERVICES, TCP_SERVICE, HILLS, BOX_COUNT, NOP_BOX,
		pad2, sshCommand, serviceAddr, servicePort, hillUrl, vpnHillAddr
	} from '$lib/finals-access';
	import { API_BASE } from '$lib/config';
	import { api, ApiError } from '$api';

	// ---- types ---------------------------------------------------------------
	// ENO raw names; Finals-3 may map to display names but we handle both.
	type SlaStatus = 'OK' | 'RECOVERING' | 'MUMBLE' | 'OFFLINE' | 'FLAG_NOT_FOUND' | 'ORG_FAULT'
	               | 'DOWN' | 'FAULTY' | 'ERROR' | 'UNKNOWN';

	type FlagFate =
		| 'held'
		| 'tide'
		| 'stolen_1'
		| 'stolen_2_3'
		| 'stolen_4_7'
		| 'stolen_8plus'
		| 'unreadable';

	interface StoreTick {
		tick: number;
		status: SlaStatus;
		credited_reason?: string | null;
		step?: string;
		code?: string;
		message?: string;
		flag_fate?: FlagFate;
	}

	interface StoreStatus {
		store_index: number;
		store_name: string;
		steal: number;
		hold: number;
		up: number;
		ticks: StoreTick[];
	}

	interface ServiceStatus {
		service_id: string;
		slug: string;
		name: string;
		port: number | null;
		stores: StoreStatus[];
		points_this_hour: { steal: number; hold: number; up: number };
		active_attackers: number;
		last_deployment?: {
			kind: string;
			org_initiated: boolean;
			tick?: number;
			deployed_at: string;
		};
		deployment_history: Array<{
			state: 'queued' | 'building' | 'gating' | 'waiting_quiet_window' | 'promoted' | 'rolled_back' | 'failed';
			message: string;
			recorded_at: string;
		}>;
	}

	interface ArenaTickInfo {
		tick: number;
		started_at: string;
		ends_at: string;
		phase: 'live' | 'paused' | 'not_started' | 'over';
	}

	interface ArenaMe {
		team_id: string;
		name: string;
		slug: string;
		team_number: number | null;
		vulnbox_ip: string | null;
		token: string;
		is_nop: boolean;
	}

	interface Alert {
		id: number;
		tick: number;
		kind: 'exploit' | 'holding' | 'shield_up';
		service_id: string;
		bucket: number | null; // 1/3/7/8 for exploit; null for holding/shield_up
		detail: { count?: number } | null;
		created_at: string;
	}

	let tickInfo: ArenaTickInfo = {
		tick: 0,
		started_at: '',
		ends_at: '1970-01-01T00:00:00Z',
		phase: 'not_started'
	};
	let services: ServiceStatus[] = [];
	let me: ArenaMe = {
		team_id: '',
		name: '',
		slug: '',
		team_number: null,
		vulnbox_ip: null,
		token: '',
		is_nop: false
	};
	let alerts: Alert[] = [];
	let latestTick = 0;

	let now = Date.now();
	let fullToken = '';
	let tokenBusy = false;
	async function revealToken() {
		if (tokenBusy) return;
		tokenBusy = true;
		try {
			fullToken = (await api.getArenaToken()).token;
		} catch {
			fullToken = '';
		} finally {
			tokenBusy = false;
		}
	}
	let submitFlag = '';
	let submitResult = '';
	let submitBusy = false;
	let refreshError = '';
	let lastUpdatedAt: number | null = null;
	let initialLoading = true;
	let refreshBusy = false;
	let clockInterval: ReturnType<typeof setInterval>;
	let pollInterval: ReturnType<typeof setInterval>;

	$: isFinalsMode = $platformInfo?.arena_enabled === true && $platformInfo?.operations_enabled === true;
	// first load waits for platform info, not the 30s poll
	let mounted = false;
	let primed = false;
	$: if (mounted && isFinalsMode && !primed) {
		primed = true;
		refresh();
	}
	$: tickEndsIn = Math.max(0, Math.round((Date.parse(tickInfo.ends_at) - now) / 1000));
	$: fmtCountdown = `${String(Math.floor(tickEndsIn / 60)).padStart(2, '0')}:${String(tickEndsIn % 60).padStart(2, '0')}`;
	$: refreshedAgo = lastUpdatedAt === null ? 'waiting for live data' : `updated ${Math.max(0, Math.round((now - lastUpdatedAt) / 1000))}s ago`;
	$: statusStale = lastUpdatedAt !== null && now - lastUpdatedAt > 75000;

	// service name lookup for alerts
	$: svcNames = new Map(services.map(s => [s.service_id, s.name]));
	$: tt = me.team_number;
	const boxes = Array.from({ length: BOX_COUNT }, (_, i) => i + 1);
	const legend: Array<[string, string]> = [
		['Up', 'bg-[#4b7355]'], ['Recovering', 'bg-[#3f6a86]'], ['Faulty', 'bg-[#9c7a30]'],
		['Down', 'bg-[#b0453a]'], ['Flag lost', 'bg-[#7a4a86]'], ['Our fault', 'bg-stone-700']
	];
	const legendHelp = 'Each square is one tick, newest on the right. Dot: flag safe. xN: stolen by about N teams. T: taken only by the NOP. ?: flag unreadable. Amber corner: credited. Our fault ticks count as up and cost nothing.';

	function authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
		const h: Record<string, string> = {};
		if (token) h['Authorization'] = `Bearer ${token}`;
		return h;
	}

	async function liveFetch(url: string, init: Parameters<typeof fetch>[1] = {}): Promise<Response> {
		const controller = new AbortController();
		const timer = window.setTimeout(() => controller.abort(), 8000);
		try {
			return await fetch(url, { ...init, signal: controller.signal });
		} finally {
			window.clearTimeout(timer);
		}
	}

	async function loadTick() {
		const res = await liveFetch(`${API_BASE}/api/v1/arena/tick`);
		if (!res.ok) throw new Error('tick unavailable');
		tickInfo = await res.json();
	}

	async function loadMe() {
		const res = await liveFetch(`${API_BASE}/api/v1/arena/me`, { headers: authHeaders() });
		if (!res.ok) throw new Error('team identity unavailable');
		me = await res.json();
	}

	async function loadStatus() {
		if (!isFinalsMode) return;
		const url = `${API_BASE}/api/v1/arena/me/status${latestTick > 0 ? `?since=${latestTick}` : ''}`;
		const res = await liveFetch(url, { headers: authHeaders() });
		if (!res.ok) throw new Error('service status unavailable');
		const d = await res.json();
		const incoming: ServiceStatus[] = d.services ?? [];

		const svcMap = new Map<string, ServiceStatus>(services.map(s => [s.service_id, s]));
		for (const svc of incoming) {
			if (!svcMap.has(svc.service_id)) {
				// new service: cap ticks
				svcMap.set(svc.service_id, {
					...svc,
					stores: svc.stores.map(st => ({ ...st, ticks: st.ticks.slice(-30) })),
				});
				continue;
			}
			const existing = svcMap.get(svc.service_id)!;
			const storeMap = new Map(existing.stores.map(s => [s.store_index, s]));
			for (const store of svc.stores) {
				const ex = storeMap.get(store.store_index);
				if (!ex) {
					storeMap.set(store.store_index, { ...store, ticks: store.ticks.slice(-30) });
				} else {
					const seen = new Set(ex.ticks.map(t => t.tick));
					const merged = [...ex.ticks, ...store.ticks.filter(t => !seen.has(t.tick))];
					merged.sort((a, b) => a.tick - b.tick);
					storeMap.set(store.store_index, {
						...store,
						ticks: merged.slice(-30),
					});
				}
			}
			svcMap.set(svc.service_id, {
				...svc,
				stores: [...storeMap.values()].sort((a, b) => a.store_index - b.store_index),
			});
		}
		const enabled = new Set(incoming.map(s => s.service_id));
		services = [...svcMap.values()].filter(s => enabled.has(s.service_id));
		lastUpdatedAt = Date.now();

		const allTickNums = services.flatMap(s => s.stores.flatMap(st => st.ticks.map(t => t.tick)));
		if (allTickNums.length) latestTick = Math.max(...allTickNums, latestTick);
	}

	async function loadAlerts() {
		if (!isFinalsMode) return;
		const res = await liveFetch(`${API_BASE}/api/v1/arena/me/alerts`, { headers: authHeaders() });
		if (!res.ok) throw new Error('alerts unavailable');
		const d = await res.json();
		alerts = d.alerts ?? [];
	}

	async function refresh() {
		if (refreshBusy) return;
		refreshBusy = true;
		try {
			const results = await Promise.allSettled([loadTick(), loadMe(), loadStatus(), loadAlerts()]);
			const failed = results.filter(r => r.status === 'rejected').length;
			refreshError = failed ? 'Some live data failed to load. Showing the last known state.' : '';
		} finally {
			initialLoading = false;
			refreshBusy = false;
		}
	}

	async function submitFlags() {
		if (!submitFlag.trim() || submitBusy) return;
		submitBusy = true;
		submitResult = '';
		try {
			const flags = submitFlag.trim().split(/[\s,]+/).filter(Boolean);
			const results: Array<{ flag: string; status: string; message: string }> = [];
			for (const flag of flags) {
				try {
					const result = await api.submitArenaFlag(flag);
					results.push({ flag, status: result.status, message: result.message ?? result.status });
				} catch (error) {
					if (error instanceof ApiError) {
						results.push({
							flag,
							status: String(error.details.status ?? 'rejected'),
							message: error.message
						});
						continue;
					}
					throw error;
				}
			}
			submitResult = results
				.map((r, index) => `Flag ${index + 1}: ${r.status}${r.message && r.message.toLowerCase() !== r.status.toLowerCase() ? ` (${r.message})` : ''}`)
				.join('\n');
			if (results.every(r => r.status === 'accepted' || r.status === 'duplicate')) submitFlag = '';
		} catch (e: any) {
			submitResult = e?.message ?? 'submit failed';
		} finally {
			submitBusy = false;
		}
	}

	// ---- display helpers -----------------------------------------------------
	function svcLatestTick(svc: ServiceStatus): StoreTick | null {
		return svc.stores[0]?.ticks.at(-1) ?? null;
	}

	// map both ENO raw names and display names to a color key
	function statusColorKey(s: SlaStatus): string {
		switch (s) {
			case 'OK': return 'bg-[#4b7355]';
			case 'RECOVERING': return 'bg-[#3f6a86]';
			case 'MUMBLE': case 'FAULTY': return 'bg-[#9c7a30]';
			case 'OFFLINE': case 'DOWN': return 'bg-[#b0453a]';
			case 'FLAG_NOT_FOUND': return 'bg-[#7a4a86]';
			case 'ORG_FAULT': return 'bg-stone-700';
			default: return 'bg-stone-800'; // ERROR/UNKNOWN
		}
	}

	function statusLabel(s: SlaStatus): string {
		switch (s) {
			case 'OK': return 'Up';
			case 'RECOVERING': return 'Recovering';
			case 'MUMBLE': case 'FAULTY': return 'Faulty';
			case 'OFFLINE': case 'DOWN': return 'Down';
			case 'FLAG_NOT_FOUND': return 'Flag lost';
			case 'ORG_FAULT': return 'Our fault';
			case 'UNKNOWN': return 'Waiting';
			default: return 'Error';
		}
	}

	function statusChipKey(s: SlaStatus): string {
		switch (s) {
			case 'OK': return 'bg-[#4b7355]/20 text-[#9bcea8]';
			case 'RECOVERING': return 'bg-[#3f6a86]/20 text-[#8bbfe0]';
			case 'MUMBLE': case 'FAULTY': return 'bg-[#9c7a30]/20 text-[#e0be72]';
			case 'OFFLINE': case 'DOWN': return 'bg-[#b0453a]/20 text-[#e08a85]';
			case 'FLAG_NOT_FOUND': return 'bg-[#7a4a86]/20 text-[#c08ae0]';
			case 'ORG_FAULT': return 'bg-stone-800 text-amber-400/70';
			default: return 'bg-stone-800 text-stone-500';
		}
	}

	function fateGlyph(fate: FlagFate | undefined): { text: string; cls: string; tip: string } {
		if (!fate) return { text: '', cls: '', tip: '' };
		if (fate === 'held') return { text: '●', cls: 'text-[#9bcea8]', tip: 'Flag safe' };
		if (fate === 'tide') return { text: 'T', cls: 'text-amber-400', tip: 'Taken only by the NOP' };
		if (fate === 'unreadable') return { text: '?', cls: 'text-stone-500', tip: 'Flag unreadable' };
		if (fate === 'stolen_1') return { text: 'x1', cls: 'text-[#e08a85]', tip: 'Stolen by 1 team' };
		if (fate === 'stolen_2_3') return { text: 'x2', cls: 'text-[#e08a85]', tip: 'Stolen by 2-3 teams' };
		if (fate === 'stolen_4_7') return { text: 'x5', cls: 'text-[#e08a85]', tip: 'Stolen by 4-7 teams' };
		return { text: 'x8', cls: 'text-[#e08a85]', tip: 'Stolen by 8+ teams' };
	}

	function bucketLabel(bucket: number | null): string {
		if (bucket === null) return '';
		if (bucket === 1) return '1 team';
		if (bucket === 3) return '2-3 teams';
		if (bucket === 7) return '4-7 teams';
		return '8+ teams';
	}

	function svcUptime(svc: ServiceStatus): number {
		const ticks = svc.stores[0]?.ticks ?? [];
		if (!ticks.length) return 0;
		return ticks.filter(t => t.status === 'OK').length / ticks.length;
	}

	function alertAge(ts: string): string {
		const s = Math.round((now - Date.parse(ts)) / 1000);
		if (s < 60) return `${s}s`;
		return `${Math.floor(s / 60)}m`;
	}

	function fmt1(n: number | undefined): string {
		if (n === undefined) return '-';
		return n.toLocaleString(undefined, { maximumFractionDigits: 1 });
	}

	function fmtTime(ts: string): string {
		return `${new Date(ts).toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' })} IST`;
	}

	function deploymentLabel(state: ServiceStatus['deployment_history'][number]['state']): string {
		return ({
			queued: 'Queued', building: 'Building', gating: 'Testing',
			waiting_quiet_window: 'Waiting', promoted: 'Live',
			rolled_back: 'Rolled back', failed: 'Failed'
		})[state];
	}

	function deploymentClass(state: ServiceStatus['deployment_history'][number]['state']): string {
		if (state === 'promoted') return 'bg-[#4b7355]/20 text-[#9bcea8]';
		if (state === 'rolled_back') return 'bg-[#3f6a86]/20 text-[#8bbfe0]';
		if (state === 'failed') return 'bg-[#b0453a]/20 text-[#e08a85]';
		return 'bg-[#9c7a30]/20 text-[#e0be72]';
	}

	onMount(() => {
		clockInterval = setInterval(() => { now = Date.now(); }, 1000);
		pollInterval = setInterval(refresh, 30000);
		mounted = true;
	});
	onDestroy(() => {
		clearInterval(clockInterval);
		clearInterval(pollInterval);
	});
</script>

<svelte:head><title>Operations - Anvil</title></svelte:head>

<!-- the layout guard bounces this page while operations is off -->
{#if isFinalsMode}

<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-6">
	<PageHeader title={me.name || 'Operations'} subtitle={tt ? `Team ${pad2(tt)}` : 'Team number not assigned yet'} compact>
		<div slot="actions" class="flex flex-wrap items-center gap-2 text-sm">
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5">
				<span class="metadata-label text-stone-500">Tick</span>
				<span class="font-semibold tabular-nums text-stone-100">{tickInfo.tick}</span>
			</div>
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5">
				<span class="metadata-label text-stone-500">Next in</span>
				<span class="font-mono tabular-nums text-stone-100">{fmtCountdown}</span>
			</div>
			{#if tickInfo.phase === 'paused'}
				<div class="flex items-center gap-1.5 rounded-md border border-[#9c7a30]/40 bg-[#9c7a30]/10 px-3 py-1.5 text-[#e0be72]">
					<Icon icon="mdi:pause-circle-outline" class="h-4 w-4" />
					<span class="font-medium">Paused</span>
				</div>
			{:else if tickInfo.phase === 'over'}
				<div class="flex items-center gap-1.5 rounded-md border border-stone-700 bg-stone-900 px-3 py-1.5 text-stone-500">
					<Icon icon="mdi:flag-checkered" class="h-4 w-4" />
					<span>Over</span>
				</div>
			{/if}
		</div>
	</PageHeader>

	<div class="mb-5 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
		<a href="/arena" class="text-stone-400 hover:text-stone-200">Arena</a>
		{#if $platformInfo?.vpn_enabled}<a href="/vpn" class="text-stone-400 hover:text-stone-200">VPN</a>{/if}
		<a href="{API_BASE}/api/v1/arena/attack-info" target="_blank" rel="noreferrer" class="text-stone-400 hover:text-stone-200">Flag IDs</a>
		<a href="/ad-kit.zip" download class="text-stone-400 hover:text-stone-200">Kit</a>
		<span class="ml-auto text-stone-600">{refreshedAgo}</span>
		<button type="button" on:click={refresh} disabled={refreshBusy} class="inline-flex items-center gap-1 rounded text-stone-400 hover:text-stone-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-stone-500 disabled:opacity-50">
			<Icon icon="mdi:refresh" class="h-3.5 w-3.5 {refreshBusy ? 'animate-spin' : ''}" />Refresh
		</button>
	</div>

	{#if refreshError}
		<div class="mb-5 rounded-md border border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-300" role="alert">{refreshError}</div>
	{:else if statusStale}
		<div class="mb-5 rounded-md border border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-300" role="status">Status is out of date. Refresh to check.</div>
	{/if}

	{#if alerts.length}
		<Card title="Alerts" bodyClass="p-0" className="mb-5">
			<span slot="meta" class="text-xs text-stone-600">1 tick behind</span>
			<ul class="divide-y divide-stone-800/60">
				{#each alerts.slice(0, 10) as a}
					{@const svcName = svcNames.get(a.service_id) ?? a.service_id}
					<li class="flex items-center gap-3 px-4 py-2 text-xs">
						{#if a.kind === 'shield_up'}
							<Icon icon="mdi:shield-check" class="h-3.5 w-3.5 shrink-0 text-[#9bcea8]" />
							<span class="w-20 whitespace-nowrap font-medium text-[#9bcea8]">Shield up</span>
						{:else if a.kind === 'holding'}
							<Icon icon="mdi:lock-check-outline" class="h-3.5 w-3.5 shrink-0 text-stone-400" />
							<span class="w-20 whitespace-nowrap font-medium text-stone-400">Holding</span>
						{:else}
							<Icon icon="mdi:alert-circle-outline" class="h-3.5 w-3.5 shrink-0 text-[#e08a85]" />
							<span class="w-20 whitespace-nowrap font-medium text-[#e08a85]">Stolen</span>
						{/if}
						<span class="min-w-0 flex-1 truncate text-stone-400">{svcName}</span>
						{#if a.bucket !== null}<span class="text-stone-500">{bucketLabel(a.bucket)}</span>{/if}
						<span class="tabular-nums text-stone-600">t{a.tick}</span>
						<span class="w-8 text-right text-stone-700">{alertAge(a.created_at)}</span>
					</li>
				{/each}
			</ul>
		</Card>
	{/if}

	<div class="grid gap-4 lg:grid-cols-3">
		<div class="min-w-0 space-y-4 lg:col-span-2">
			<Card title="Your box">
				{#if tt}
					<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-950/60 px-3 py-2 text-sm">
						<span class="select-none font-mono text-stone-600">$</span>
						<CopyText text={sshCommand(tt)} wrap className="min-w-0 text-stone-200" />
					</div>
					<p class="mt-2 text-xs text-stone-500">Uses your team's registered ed25519 key.</p>
					<ul class="mt-4 grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
						{#each SERVICES as name, i}
							<li class="flex items-center justify-between gap-3">
								<span class="min-w-0 truncate text-stone-400">
									<span class="mr-1.5 tabular-nums text-stone-600">{i + 1}</span>{name}
									{#if i + 1 === TCP_SERVICE}<span class="ml-1 text-[0.65rem] uppercase text-stone-600">tcp</span>{/if}
								</span>
								<CopyText text={serviceAddr(tt, i + 1)} className="shrink-0 text-xs text-stone-300 hover:text-stone-100" />
							</li>
						{/each}
					</ul>
					{#if PUBLIC_ACCESS}<p class="mt-4 font-mono text-[0.7rem] text-stone-600">via VPN: {me.vulnbox_ip ?? `10.60.${tt}.2`} ports 7001-7006</p>{:else}<p class="mt-4 text-xs text-stone-600">VPN required</p>{/if}
				{:else}
					<p class="text-sm text-stone-500">No team number yet. Ask an organizer.</p>
				{/if}
			</Card>

			<Card title="Targets">
				<span slot="meta" class="flex items-center gap-1.5 text-xs text-stone-500">{#if PUBLIC_ACCESS}host <CopyText text={PUBLIC_HOST} className="text-stone-300" />{:else}box NN = 10.60.NN.2{/if}</span>
				<div class="-mx-1 overflow-x-auto">
					<table class="w-full text-xs">
						<thead>
							<tr class="text-left align-bottom text-stone-500">
								<th class="px-1.5 pb-2 font-medium">Box</th>
								{#each SERVICES as name, i}
									<th class="min-w-[4.5rem] px-1.5 pb-2 font-medium leading-tight"><span class="text-stone-600">{i + 1}</span> {name}</th>
								{/each}
							</tr>
						</thead>
						<tbody>
							{#each boxes as bb}
								<tr class="border-t border-stone-800/60 {bb === tt ? 'bg-stone-800/50' : ''}">
									<td class="whitespace-nowrap px-1.5 py-1.5 font-mono text-stone-300">
										{pad2(bb)}
										{#if bb === tt}<span class="ml-1 rounded bg-stone-700 px-1 font-sans text-[0.65rem] text-stone-200">you</span>{/if}
										{#if bb === NOP_BOX}<span class="ml-1 rounded bg-amber-500/15 px-1 font-sans text-[0.65rem] text-amber-300">NOP</span>{/if}
									</td>
									{#each SERVICES as _, i}
										<td class="px-1.5 py-1">
											<CopyText text={serviceAddr(bb, i + 1)} label={String(servicePort(bb, i + 1))} quiet className="tabular-nums text-stone-400 hover:text-stone-100" />
										</td>
									{/each}
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<p class="mt-3 text-xs text-stone-600">Click a port to copy host:port. Box {NOP_BOX} is the NOP, a practice target.</p>
			</Card>
		</div>

		<div class="min-w-0 space-y-4">
			<Card title="Submit flags">
				<form on:submit|preventDefault={submitFlags} class="flex flex-col gap-2">
					<textarea
						bind:value={submitFlag}
						aria-label="Captured flags"
						rows="4"
						placeholder="H7CTF&#123;...&#125; one per line"
						class="w-full resize-none rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2 text-sm font-mono text-stone-200 placeholder-stone-600 focus:border-stone-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-stone-500 transition-colors"
						autocomplete="off" spellcheck="false"
					></textarea>
					<button
						type="submit"
						disabled={submitBusy || !submitFlag.trim() || tickInfo.phase !== 'live'}
						class="rounded-md bg-stone-100 text-stone-950 font-medium py-2 text-sm hover:bg-stone-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-stone-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
					>
						{submitBusy ? 'Submitting...' : tickInfo.phase !== 'live' ? 'Game not live' : 'Submit'}
					</button>
					{#if submitResult}
						<pre class="text-xs font-mono text-stone-400 whitespace-pre-wrap break-all" role="status" aria-live="polite">{submitResult}</pre>
					{/if}
				</form>
				{#if me.token}
					<div class="mt-3 flex min-w-0 items-center gap-1.5 text-[0.7rem] text-stone-600" title="Send as the X-Team-Token header from scripts">
						<span class="shrink-0">Team token</span>
						{#if fullToken}
							<CopyText text={fullToken} wrap className="min-w-0 font-mono text-stone-300" />
						{:else}
							<span class="font-mono text-stone-500">{me.token}</span>
							<button type="button" on:click={revealToken} disabled={tokenBusy} class="text-stone-400 underline hover:text-stone-200 disabled:opacity-50">show</button>
						{/if}
					</div>
				{/if}
			</Card>

			<Card title="King of the Hill">
				{#if tt}
					<ul class="space-y-2.5 text-sm">
						{#each HILLS as name, i}
							<li class="flex flex-wrap items-center justify-between gap-x-3 gap-y-0.5">
								<span class="text-stone-400"><span class="mr-1.5 tabular-nums text-stone-600">{i + 1}</span>{name}</span>
								<CopyText text={hillUrl(tt, i + 1)} className="text-xs text-stone-300 hover:text-stone-100" />
							</li>
						{/each}
					</ul>
					{#if PUBLIC_ACCESS}
						<p class="mt-3 text-xs text-stone-500">These ports are your team's own. Use only these.</p>
						<p class="mt-2 font-mono text-[0.7rem] text-stone-600">via VPN: {HILLS.map((_, i) => vpnHillAddr(i + 1)).join(' · ')}</p>
					{/if}
				{:else}
					<p class="text-sm text-stone-500">No team number yet. Ask an organizer.</p>
				{/if}
			</Card>
		</div>
	</div>

	<Card title="Service status" bodyClass="p-0" className="mt-4">
		<svelte:fragment slot="meta">
			<div class="hidden flex-wrap items-center gap-x-3 text-[0.7rem] text-stone-500 md:flex">
				{#each legend as [lbl, cls]}
					<span class="inline-flex items-center gap-1"><span class="h-2 w-2 rounded-sm {cls}"></span>{lbl}</span>
				{/each}
			</div>
			<HelpTip text={legendHelp} label="Status key" align="right" />
		</svelte:fragment>
		{#if initialLoading}
			<div class="flex items-center justify-center gap-2 px-4 py-8 text-sm text-stone-500" role="status"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />Loading...</div>
		{:else if services.length === 0}
			<div class="px-4 py-8 text-center text-sm text-stone-500">No services yet. Refresh, or ask an organizer if this stays empty.</div>
		{/if}
		<ul class="divide-y divide-stone-800/60">
			{#each services as svc (svc.service_id)}
				{@const latestT = svcLatestTick(svc)}
				{@const svcStatus = latestT?.status ?? 'UNKNOWN'}
				{@const deployment = svc.deployment_history?.[0]}
				<li class="px-4 py-3">
					<div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
						<span class="text-sm font-medium text-stone-200">{svc.name}</span>
						<span class="rounded px-1.5 py-0.5 text-xs font-medium {statusChipKey(svcStatus)}">{statusLabel(svcStatus)}</span>
						{#if latestT}<span class="text-xs text-stone-500">{(svcUptime(svc) * 100).toFixed(1)}% up</span>{/if}
						{#if svc.active_attackers}<span class="text-xs text-stone-500">{svc.active_attackers} attacking</span>{/if}
						{#if deployment}
							<span class="rounded px-1.5 py-0.5 text-xs font-medium {deploymentClass(deployment.state)}" title="{deployment.message} ({fmtTime(deployment.recorded_at)})">{deploymentLabel(deployment.state)}</span>
						{:else if svc.last_deployment}
							<span class="text-xs text-stone-600" title={svc.last_deployment.deployed_at}>{svc.last_deployment.kind} at {fmtTime(svc.last_deployment.deployed_at)}</span>
						{/if}
						{#if latestT?.message}
							<span class="max-w-xs truncate text-xs text-stone-600" title={latestT.message}>{latestT.message}</span>
						{/if}
					</div>
					{#each svc.stores as store (store.store_index)}
						{@const fate = store.ticks.at(-1)?.flag_fate}
						{@const g = fateGlyph(fate)}
						<div class="mb-1 flex items-center gap-2">
							<span class="w-20 shrink-0 truncate text-xs text-stone-500" title={store.store_name}>{store.store_name}</span>
							<div class="flex gap-px overflow-x-auto pb-0.5">
								{#each store.ticks as t}
									{@const credited = !!t.credited_reason}
									<div
										class="relative h-4 w-4 shrink-0 rounded-sm {statusColorKey(t.status)} {credited ? 'ring-1 ring-amber-400/50' : ''}"
										title="{statusLabel(t.status)} t{t.tick}{t.credited_reason ? ' (credited: ' + t.credited_reason + ')' : ''}{t.message ? ' - ' + t.message : ''}"
										role="img"
										aria-label="{statusLabel(t.status)} at tick {t.tick}{t.credited_reason ? ', credited: ' + t.credited_reason : ''}"
									>
										{#if credited}
											<span class="absolute top-0 right-0 h-1 w-1 rounded-full bg-amber-400"></span>
										{/if}
									</div>
								{/each}
								{#if store.ticks.length === 0}
									<span class="text-xs italic text-stone-600">no data yet</span>
								{/if}
							</div>
							{#if g.text}
								<span class="shrink-0 text-[0.8rem] {g.cls}" title={g.tip}>{g.text}</span>
							{/if}
							<span class="ml-auto shrink-0 text-xs tabular-nums text-[#9bcea8]">+{fmt1(store.hold)} hold</span>
						</div>
					{/each}
					{#if svc.deployment_history?.length > 1}
						<details class="mt-1 text-xs text-stone-500">
							<summary class="w-fit cursor-pointer rounded text-stone-500 hover:text-stone-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-stone-500">Deploy history</summary>
							<ol class="mt-2 space-y-1 border-l border-stone-800 pl-3">
								{#each svc.deployment_history as event}
									<li><span class="text-stone-300">{deploymentLabel(event.state)}</span> · {event.message} · {fmtTime(event.recorded_at)}</li>
								{/each}
							</ol>
						</details>
					{/if}
				</li>
			{/each}
		</ul>
	</Card>
</div>

{/if}
