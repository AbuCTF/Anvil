<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { platformInfo } from '$lib/stores/platform';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import { API_BASE } from '$lib/config';

	// ---- types ---------------------------------------------------------------
	type SlaStatus = 'OK' | 'RECOVERING' | 'MUMBLE' | 'OFFLINE' | 'FLAG_NOT_FOUND' | 'ORG_FAULT';
	type FlagFate = 'held' | 'safe' | 'stolen_1' | 'stolen_2_3' | 'stolen_4_7' | 'stolen_8plus' | 'tide' | 'unreadable' | null;
	interface TickRow {
		tick: number;
		status: SlaStatus;
		effective_status?: SlaStatus; // ORG_FAULT ticks get effective UP for scoring
		credited_reason?: string | null;
		step: string | null;
		code: string | null;
		message: string | null;
		flag_fate: FlagFate;
		steal: number;
		hold: number;
		up: number;
	}
	interface StoreRow {
		store_index: number;
		store_name: string;
		steal: number;
		hold: number;
		up: number;
		flags_held: number;
		flags_settled: number;
		ticks: TickRow[];
	}
	interface ServiceRow {
		slug: string;
		name: string;
		port: number;
		stores: StoreRow[];
		steal: number;
		hold: number;
		up: number;
		active_attackers: number;
	}
	interface ArenaMe {
		team_name: string;
		vulnbox_ip: string;
		exploiter_ip: string;
		submit_token: string;
		reset_quota: number;
		reset_next_at: string | null;
	}
	interface ArenaTickInfo {
		tick: number;
		started_at: string;
		ends_at: string;
		hold_from: string;
	}
	interface Alert {
		store: string;
		planted_tick: number;
		bucket: '1' | '2-3' | '4-7' | '8+';
		holding: boolean;
		published_at: string;
	}
	interface ResetStatus {
		status: 'idle' | 'requested' | 'restoring' | 'booting' | 'up';
		next_allowed_at: string | null;
	}

	// ---- mock data -----------------------------------------------------------
	// used until M2 (real API wired). representative of 15-team 6-service finals.
	const MOCK_TICK: ArenaTickInfo = {
		tick: 87,
		started_at: new Date(Date.now() - 73000).toISOString(),
		ends_at: new Date(Date.now() + 47000).toISOString(),
		hold_from: new Date(Date.now() + 37000).toISOString()
	};

	function mkTicks(base: number, count = 30): TickRow[] {
		const statuses: SlaStatus[] = ['OK','OK','OK','OK','OK','OK','OK','OK','OK','RECOVERING','OK','OK','MUMBLE','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK','OK'];
		const fates: FlagFate[] = ['held','held','held','held','held','safe','held','stolen_2_3','held','unreadable','held','held','stolen_1','held','held','held','held','held','held','held','safe','held','held','held','held','held','safe','held','held','held'];
		return Array.from({ length: count }, (_, i) => ({
			tick: base - (count - 1 - i),
			status: statuses[i % statuses.length],
			step: statuses[i % statuses.length] === 'MUMBLE' ? 'havoc' : null,
			code: statuses[i % statuses.length] === 'MUMBLE' ? 'HARBORID-HAVOC-04' : null,
			message: statuses[i % statuses.length] === 'MUMBLE' ? 'search by tag returned no results for an existing note' : null,
			flag_fate: fates[i % fates.length],
			steal: 0,
			hold: fates[i % fates.length] === 'held' ? 9.8 : 0,
			up: statuses[i % statuses.length] !== 'OFFLINE' ? 3.87 : 0
		}));
	}

	const MOCK_SERVICES: ServiceRow[] = [
		{
			slug: 'harbor-id', name: 'Harbor ID', port: 8001,
			steal: 18.2, hold: 127.4, up: 46.4, active_attackers: 6,
			stores: [
				{ store_index: 0, store_name: 'creds', steal: 12.1, hold: 87.3, up: 23.2, flags_held: 24, flags_settled: 28, ticks: mkTicks(87) },
				{ store_index: 1, store_name: 'sessions', steal: 6.1, hold: 40.1, up: 23.2, flags_held: 18, flags_settled: 30,
					ticks: mkTicks(87).map((t, i) => ({
						...t, status: (i === 5 ? 'OFFLINE' : 'OK') as SlaStatus,
						effective_status: (i === 12 ? 'ORG_FAULT' : undefined) as SlaStatus | undefined,
						credited_reason: i === 12 ? 'nop box failed same step (9 teams)' : null,
						flag_fate: i === 5 ? null : (i % 4 === 0 ? 'stolen_4_7' : 'held') as FlagFate,
						hold: i % 4 === 0 ? 0 : 9.8
					}))
				}
			]
		},
		{
			slug: 'tidepay', name: 'TidePay', port: 8002,
			steal: 0, hold: 87.3, up: 46.4, active_attackers: 4,
			stores: [
				{ store_index: 0, store_name: 'balance', steal: 0, hold: 52.0, up: 23.2, flags_held: 22, flags_settled: 30,
					ticks: mkTicks(87).map((t, i) => ({ ...t, status: 'OK' as SlaStatus, flag_fate: i % 3 === 0 ? 'tide' : 'held' as FlagFate, hold: i % 3 === 0 ? 0 : 9.8 })) },
				{ store_index: 1, store_name: 'txlog', steal: 0, hold: 35.3, up: 23.2, flags_held: 28, flags_settled: 30,
					ticks: mkTicks(87).map((t) => ({ ...t, status: 'OK' as SlaStatus, flag_fate: 'held' as FlagFate, hold: 9.8 })) }
			]
		},
		{
			slug: 'bollard', name: 'Bollard', port: 8003,
			steal: 12.1, hold: 0, up: 31.2, active_attackers: 8,
			stores: [
				{ store_index: 0, store_name: 'firmware', steal: 12.1, hold: 0, up: 15.6, flags_held: 0, flags_settled: 22,
					ticks: mkTicks(87).map((t, i) => ({ ...t, flag_fate: i < 5 ? 'unreadable' : 'safe' as FlagFate, hold: 0, status: i < 5 ? 'FLAG_NOT_FOUND' : 'OK' as SlaStatus })) },
				{ store_index: 1, store_name: 'config', steal: 0, hold: 0, up: 15.6, flags_held: 0, flags_settled: 30,
					ticks: mkTicks(87).map((t) => ({ ...t, status: 'OK' as SlaStatus, flag_fate: 'safe' as FlagFate, hold: 0 })) }
			]
		}
	];

	const MOCK_ME: ArenaMe = {
		team_name: 'ByteBrigade',
		vulnbox_ip: '10.10.42.1',
		exploiter_ip: '10.10.43.1',
		submit_token: '••••••••••••••••',
		reset_quota: 2,
		reset_next_at: null
	};

	const MOCK_ALERTS: Alert[] = [
		{ store: 'harbor-id/creds', planted_tick: 85, bucket: '2-3', holding: false, published_at: new Date(Date.now() - 130000).toISOString() },
		{ store: 'bollard/firmware', planted_tick: 84, bucket: '8+', holding: false, published_at: new Date(Date.now() - 250000).toISOString() },
		{ store: 'harbor-id/sessions', planted_tick: 83, bucket: '4-7', holding: true, published_at: new Date(Date.now() - 370000).toISOString() }
	];

	// ---- state ---------------------------------------------------------------
	let tickInfo: ArenaTickInfo = MOCK_TICK;
	let services: ServiceRow[] = MOCK_SERVICES;
	let me: ArenaMe = MOCK_ME;
	let alerts: Alert[] = MOCK_ALERTS;
	let resetStatus: ResetStatus = { status: 'idle', next_allowed_at: null };

	let now = Date.now();
	let submitFlag = '';
	let submitResult = '';
	let submitBusy = false;
	let resetBusy = false;
	let expandedService: string | null = null;
	let clockInterval: ReturnType<typeof setInterval>;
	let pollInterval: ReturnType<typeof setInterval>;

	$: isFinalsMode = $platformInfo?.game_state === 'running' || $platformInfo?.game_state === 'paused' || $platformInfo?.game_state === 'stopped';
	$: tickEndsIn = Math.max(0, Math.round((Date.parse(tickInfo.ends_at) - now) / 1000));
	$: holdActive = now >= Date.parse(tickInfo.hold_from);
	$: fmtCountdown = `${String(Math.floor(tickEndsIn / 60)).padStart(2, '0')}:${String(tickEndsIn % 60).padStart(2, '0')}`;

	function authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
		return token ? { Authorization: `Bearer ${token}` } : {};
	}

	async function loadStatus() {
		if (!isFinalsMode) return;
		try {
			const [tickRes, statusRes, alertRes] = await Promise.all([
				fetch(`${API_BASE}/api/v1/arena/tick`, { headers: authHeaders() }),
				fetch(`${API_BASE}/api/v1/arena/me/status`, { headers: authHeaders() }),
				fetch(`${API_BASE}/api/v1/arena/me/alerts`, { headers: authHeaders() })
			]);
			if (tickRes.ok) tickInfo = await tickRes.json();
			if (statusRes.ok) { const d = await statusRes.json(); services = d.services ?? services; }
			if (alertRes.ok) { const d = await alertRes.json(); alerts = d.alerts ?? alerts; }
		} catch { /* keep mock data */ }
	}

	async function submitFlags() {
		if (!submitFlag.trim() || submitBusy) return;
		submitBusy = true;
		submitResult = '';
		try {
			const flags = submitFlag.trim().split(/\s+/);
			const res = await fetch(`${API_BASE}/api/v1/arena/flags`, {
				method: 'PUT',
				headers: { ...authHeaders(), 'Content-Type': 'application/json' },
				body: JSON.stringify({ flags })
			});
			const d = await res.json();
			submitResult = d.results?.map((r: any) => `${r.flag}: ${r.status}`).join('\n') ?? (res.ok ? 'submitted' : 'error');
			if (res.ok) submitFlag = '';
		} catch (e: any) {
			submitResult = e?.message ?? 'submit failed';
		} finally {
			submitBusy = false;
		}
	}

	async function resetVulnbox() {
		if (resetBusy) return;
		resetBusy = true;
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/me/vulnbox/reset`, {
				method: 'POST',
				headers: authHeaders()
			});
			if (res.ok) {
				resetStatus = { status: 'requested', next_allowed_at: null };
			}
		} catch { /* ignore */ } finally {
			resetBusy = false;
		}
	}

	// ---- display helpers -----------------------------------------------------
	const statusColor: Record<SlaStatus, string> = {
		OK: 'bg-[#4b7355]',
		RECOVERING: 'bg-[#3f6a86]',
		MUMBLE: 'bg-[#9c7a30]',
		OFFLINE: 'bg-[#b0453a]',
		FLAG_NOT_FOUND: 'bg-[#7a4a86]',
		ORG_FAULT: 'bg-stone-700'
	};
	const statusLabel: Record<SlaStatus, string> = {
		OK: 'OK', RECOVERING: 'RECOVERING', MUMBLE: 'MUMBLE',
		OFFLINE: 'OFFLINE', FLAG_NOT_FOUND: 'FLAG NOT FOUND', ORG_FAULT: 'ORG FAULT'
	};
	const statusChipColor: Record<SlaStatus, string> = {
		OK: 'bg-[#4b7355]/20 text-[#9bcea8]',
		RECOVERING: 'bg-[#3f6a86]/20 text-[#8bbfe0]',
		MUMBLE: 'bg-[#9c7a30]/20 text-[#e0be72]',
		OFFLINE: 'bg-[#b0453a]/20 text-[#e08a85]',
		FLAG_NOT_FOUND: 'bg-[#7a4a86]/20 text-[#c08ae0]',
		ORG_FAULT: 'bg-stone-800 text-stone-500'
	};

	function fateGlyph(fate: FlagFate): { text: string; cls: string } {
		if (!fate) return { text: '', cls: '' };
		switch (fate) {
			case 'held': return { text: '●', cls: 'text-[#9bcea8]' };
			case 'safe': return { text: '○', cls: 'text-stone-400' };
			case 'stolen_1': return { text: 'x1', cls: 'text-[#e08a85]' };
			case 'stolen_2_3': return { text: 'x2', cls: 'text-[#e08a85]' };
			case 'stolen_4_7': return { text: 'x5', cls: 'text-[#e08a85]' };
			case 'stolen_8plus': return { text: 'x8', cls: 'text-[#e08a85]' };
			case 'tide': return { text: 'T', cls: 'text-amber-400' };
			case 'unreadable': return { text: '?', cls: 'text-warn' };
		}
	}

	function latestCheck(store: StoreRow): TickRow | null {
		return store.ticks.at(-1) ?? null;
	}

	function svcUptime(svc: ServiceRow): number {
		const all = svc.stores.flatMap(s => s.ticks);
		if (!all.length) return 0;
		return all.filter(t => (t.effective_status ?? t.status) === 'OK').length / all.length;
	}

	function alertAge(a: Alert): string {
		const s = Math.round((now - Date.parse(a.published_at)) / 1000);
		if (s < 60) return `${s}s ago`;
		return `${Math.floor(s / 60)}m ago`;
	}

	function fmt1(n: number): string {
		return n.toLocaleString(undefined, { maximumFractionDigits: 1 });
	}

	onMount(() => {
		clockInterval = setInterval(() => { now = Date.now(); }, 1000);
		pollInterval = setInterval(loadStatus, 30000);
		loadStatus();
	});
	onDestroy(() => {
		clearInterval(clockInterval);
		clearInterval(pollInterval);
	});
</script>

<svelte:head><title>Team Status - Anvil</title></svelte:head>

{#if !isFinalsMode}
	<!-- redirect to the quals team page - this route is finals-only -->
	<script>window.location.replace('/team');</script>
{:else}

<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-6">
	<!-- tick clock header -->
	<div class="mb-5 flex flex-wrap items-center gap-3">
		<PageHeader title="Team Status" compact />
		<div class="ml-auto flex flex-wrap items-center gap-2">
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5 text-sm">
				<span class="metadata-label text-stone-500">Tick</span>
				<span class="font-semibold tabular-nums text-stone-100">{tickInfo.tick}</span>
			</div>
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5 text-sm">
				<span class="metadata-label text-stone-500">Ends in</span>
				<span class="font-mono tabular-nums text-stone-100">{fmtCountdown}</span>
			</div>
			{#if holdActive}
				<div class="flex items-center gap-1.5 rounded-md border border-[#4b7355]/40 bg-[#4b7355]/10 px-3 py-1.5 text-sm text-[#9bcea8] animate-pulse">
					<Icon icon="mdi:shield-check-outline" class="h-4 w-4" />
					<span class="font-medium">SAFE TO RESTART</span>
				</div>
			{/if}
		</div>
	</div>

	<!-- exploitation alerts -->
	{#if alerts.length}
		<div class="mb-5 rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden">
			<div class="px-4 py-2.5 border-b border-stone-800 flex items-center gap-2">
				<Icon icon="mdi:bell-ring-outline" class="h-4 w-4 text-stone-500" />
				<span class="text-sm font-medium text-stone-300">Exploitation alerts</span>
				<span class="ml-auto metadata-label text-stone-600">1 tick late, bucketed</span>
			</div>
			<ul class="divide-y divide-stone-800/60">
				{#each alerts as a}
					<li class="flex items-center gap-3 px-4 py-2.5 text-sm">
						<span class="w-1.5 h-1.5 rounded-full shrink-0 {a.holding ? 'bg-[#4b7355]' : 'bg-[#b0453a]'}"></span>
						<span class="text-stone-400 min-w-0 flex-1 truncate">{a.store}</span>
						<span class="text-stone-500 tabular-nums">tick {a.planted_tick}</span>
						{#if a.holding}
							<span class="text-xs text-[#9bcea8] font-medium">holding</span>
						{:else}
							<span class="text-xs text-[#e08a85]">{a.bucket} teams</span>
						{/if}
						<span class="text-xs text-stone-600">{alertAge(a)}</span>
					</li>
				{/each}
			</ul>
		</div>
	{/if}

	<!-- services -->
	{#each services as svc (svc.slug)}
		{@const lastStore0 = latestCheck(svc.stores[0])}
		{@const svcStatus = lastStore0?.status ?? 'OK'}
		<div class="mb-5 rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden">
			<!-- service header -->
			<div class="flex flex-wrap items-center gap-3 px-4 py-3 border-b border-stone-800">
				<button
					class="flex items-center gap-2 text-left"
					on:click={() => expandedService = expandedService === svc.slug ? null : svc.slug}
					aria-expanded={expandedService === svc.slug}
				>
					<Icon icon={expandedService === svc.slug ? 'mdi:chevron-down' : 'mdi:chevron-right'} class="h-4 w-4 text-stone-500 shrink-0" />
					<span class="text-[0.95rem] font-semibold text-stone-200">{svc.name}</span>
					<span class="text-xs text-stone-600">:{svc.port}</span>
				</button>
				<span class="rounded px-1.5 py-0.5 text-xs font-medium {statusChipColor[svcStatus]}">{svcStatus}</span>
				<span class="text-xs text-stone-600">{(svcUptime(svc) * 100).toFixed(1)}% up (30 ticks)</span>
				<div class="ml-auto flex items-center gap-4 text-xs tabular-nums">
					<span class="text-[#e08a85]">STEAL <span class="font-semibold">{fmt1(svc.steal)}</span></span>
					<span class="text-[#9bcea8]">HOLD <span class="font-semibold">{fmt1(svc.hold)}</span></span>
					<span class="text-stone-400">UP <span class="font-semibold">{fmt1(svc.up)}</span></span>
				</div>
			</div>

			<!-- stores -->
			{#each svc.stores as store (store.store_index)}
				{@const latest = latestCheck(store)}
				<div class="px-4 py-3 {store.store_index < svc.stores.length - 1 ? 'border-b border-stone-800/60' : ''}">
					<div class="flex items-center gap-2 mb-2">
						<span class="metadata-label text-stone-500 text-xs w-20 shrink-0">{store.store_name}</span>
						<div class="flex gap-px overflow-x-auto">
							{#each store.ticks as t}
								{@const displayStatus = t.effective_status ?? t.status}
								{@const g = fateGlyph(t.flag_fate)}
								{@const isCredited = !!t.credited_reason}
								<div
									class="relative flex h-5 w-5 shrink-0 items-center justify-center rounded-sm {statusColor[displayStatus]} transition-opacity {isCredited ? 'ring-1 ring-amber-400/50' : ''}"
									title="{statusLabel[displayStatus]}{isCredited ? ' (credited: ' + t.credited_reason + ')' : ''} · t{t.tick}{t.message ? ' · ' + t.message : ''}"
								>
									{#if isCredited}
										<span class="absolute top-0 right-0 h-1 w-1 rounded-full bg-amber-400"></span>
									{/if}
									{#if g.text}
										<span class="text-[0.45rem] leading-none font-bold {g.cls}">{g.text}</span>
									{/if}
								</div>
							{/each}
						</div>
					</div>
					{#if latest?.message}
						<div class="flex items-start gap-2 mt-1 text-xs text-stone-500">
							<span class="shrink-0 font-mono text-stone-600">{latest.code}</span>
							<span class="text-stone-400">{latest.step}</span>
							<span class="text-stone-500">{latest.message}</span>
						</div>
					{/if}
					<div class="mt-1.5 text-xs text-stone-600">
						held <span class="text-stone-400 tabular-nums">{store.flags_held}/{store.flags_settled}</span> flags
						against <span class="text-stone-400 tabular-nums">{svc.active_attackers}</span> active attackers
						<span class="ml-2 text-[#9bcea8] tabular-nums">+{fmt1(store.hold)} HOLD</span>
					</div>
				</div>
			{/each}

			<!-- expanded: attack-info shortcut -->
			{#if expandedService === svc.slug}
				<div class="border-t border-stone-800/60 px-4 py-2.5 flex flex-wrap gap-3 text-xs text-stone-500">
					<a href="{API_BASE}/api/v1/arena/attack-info" class="flex items-center gap-1 hover:text-amber-400 transition-colors" target="_blank" rel="noreferrer">
						<Icon icon="mdi:flag-outline" class="h-3.5 w-3.5" /> Attack info (flag IDs)
					</a>
					<span>·</span>
					<a href="http://{me.exploiter_ip}:8888" class="flex items-center gap-1 hover:text-amber-400 transition-colors" target="_blank" rel="noreferrer">
						<Icon icon="mdi:fish-outline" class="h-3.5 w-3.5" /> Tulip (traffic)
					</a>
				</div>
			{/if}
		</div>
	{/each}

	<!-- legend -->
	<div class="mb-5 flex flex-wrap gap-x-4 gap-y-1.5 text-[0.7rem] text-stone-600">
		{#each [['OK','bg-[#4b7355]'],['RECOVERING','bg-[#3f6a86]'],['MUMBLE','bg-[#9c7a30]'],['OFFLINE','bg-[#b0453a]'],['FLAG NOT FOUND','bg-[#7a4a86]'],['ORG FAULT','bg-stone-700']] as [lbl, cls]}
			<span class="inline-flex items-center gap-1"><span class="h-2.5 w-2.5 rounded-sm {cls}"></span>{lbl}</span>
		{/each}
		<span class="ml-4 inline-flex items-center gap-1 text-[#9bcea8]">● held</span>
		<span class="inline-flex items-center gap-1 text-stone-400">○ safe</span>
		<span class="inline-flex items-center gap-1 text-[#e08a85]">x2 stolen</span>
		<span class="inline-flex items-center gap-1 text-amber-400">T tide bot</span>
		<span class="inline-flex items-center gap-1 text-warn">? unreadable</span>
		<span class="ml-4 inline-flex items-center gap-1"><span class="relative h-2.5 w-2.5 rounded-sm bg-stone-700"><span class="absolute top-0 right-0 h-1 w-1 rounded-full bg-amber-400"></span></span><span class="text-amber-400/70">credited</span></span>
	</div>

	<div class="grid gap-4 sm:grid-cols-2">
		<!-- flag submission -->
		<Card title="Submit flags">
			<form on:submit|preventDefault={submitFlags} class="flex flex-col gap-2">
				<textarea
					bind:value={submitFlag}
					rows="3"
					placeholder="H7CTF&#123;...&#125; one per line or space-separated"
					class="w-full resize-none rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2 text-sm font-mono text-stone-200 placeholder-stone-600 focus:border-stone-600 focus:outline-none transition-colors"
					autocomplete="off" autocorrect="off" spellcheck="false"
				></textarea>
				<button
					type="submit"
					disabled={submitBusy || !submitFlag.trim()}
					class="rounded-md bg-stone-100 text-stone-950 font-medium py-2 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{submitBusy ? 'Submitting...' : 'Submit'}
				</button>
				{#if submitResult}
					<pre class="text-xs font-mono text-stone-400 whitespace-pre-wrap">{submitResult}</pre>
				{/if}
			</form>
		</Card>

		<!-- vulnbox actions -->
		<Card title="Vulnbox">
			<div class="space-y-3 text-sm">
				<div class="flex justify-between text-stone-500">
					<span>IP</span>
					<span class="font-mono text-stone-200">{me.vulnbox_ip}</span>
				</div>
				<div class="flex justify-between text-stone-500">
					<span>Exploiter VM</span>
					<span class="font-mono text-stone-200">{me.exploiter_ip}</span>
				</div>
				<div class="flex justify-between text-stone-500">
					<span>Reset quota</span>
					<span class="text-stone-200">{me.reset_quota} remaining</span>
				</div>
				{#if resetStatus.status !== 'idle'}
					<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2 text-xs text-stone-400">
						<Icon icon="mdi:loading" class="h-4 w-4 animate-spin text-stone-500" />
						{resetStatus.status}
					</div>
				{/if}
				<div class="flex gap-2 pt-1">
					<button
						on:click={resetVulnbox}
						disabled={resetBusy || me.reset_quota === 0 || resetStatus.status !== 'idle'}
						class="flex-1 rounded-md border border-stone-800 bg-stone-900/60 py-2 text-xs text-stone-300 hover:bg-stone-800/60 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
					>
						Reset to snapshot
					</button>
					<a
						href="http://{me.vulnbox_ip}:22"
						class="flex-1 rounded-md border border-stone-800 bg-stone-900/60 py-2 text-xs text-center text-stone-300 hover:bg-stone-800/60 transition-colors"
					>
						SSH
					</a>
				</div>
				{#if me.reset_quota === 0}
					<p class="text-xs text-stone-600">Reset quota used. Charged as normal downtime, no grace period.</p>
				{/if}
			</div>
		</Card>
	</div>

	<!-- ORG_FAULT rule -->
	<details class="mt-5 rounded-lg border border-stone-800 bg-stone-900/30 overflow-hidden">
		<summary class="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm text-stone-400 hover:text-stone-300 transition-colors select-none">
			<Icon icon="mdi:information-outline" class="h-4 w-4 shrink-0 text-stone-600" />
			<span class="font-medium">Our faults rule</span>
			<Icon icon="mdi:chevron-down" class="h-4 w-4 shrink-0 text-stone-600 ml-auto details-chevron" />
		</summary>
		<div class="border-t border-stone-800 px-4 py-3 text-xs text-stone-400 leading-relaxed space-y-2">
			<p>Our faults are credited, never charged. A tick counts as ORG_FAULT and scores as up, for UP and for the hold life rule, when:</p>
			<ul class="ml-4 space-y-1 list-disc list-outside text-stone-500">
				<li>our checker itself fails;</li>
				<li>the untouched NOP box fails the same check step as 3 or more teams in the same tick;</li>
				<li>the Outbreak wave crashes your service; or</li>
				<li>organizers re-credit it after review.</li>
			</ul>
			<p>Every credited tick shows on your status page with a credited mark and its reason.</p>
		</div>
	</details>
</div>

{/if}
