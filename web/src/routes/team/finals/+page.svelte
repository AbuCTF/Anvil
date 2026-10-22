<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { platformInfo } from '$lib/stores/platform';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import { API_BASE } from '$lib/config';

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
	let submitFlag = '';
	let submitResult = '';
	let submitBusy = false;
	let clockInterval: ReturnType<typeof setInterval>;
	let pollInterval: ReturnType<typeof setInterval>;

	$: isFinalsMode = $platformInfo?.game_state === 'running' || $platformInfo?.game_state === 'paused' || $platformInfo?.game_state === 'stopped';
	$: tickEndsIn = Math.max(0, Math.round((Date.parse(tickInfo.ends_at) - now) / 1000));
	$: fmtCountdown = `${String(Math.floor(tickEndsIn / 60)).padStart(2, '0')}:${String(tickEndsIn % 60).padStart(2, '0')}`;

	// service name lookup for alerts
	$: svcNames = new Map(services.map(s => [s.service_id, s.name]));

	function authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined' ? localStorage.getItem('accessToken') : null;
		const teamToken = typeof localStorage !== 'undefined' ? localStorage.getItem('teamToken') : null;
		const h: Record<string, string> = {};
		if (token) h['Authorization'] = `Bearer ${token}`;
		if (teamToken) h['X-Team-Token'] = teamToken;
		return h;
	}

	async function loadTick() {
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/tick`);
			if (res.ok) tickInfo = await res.json();
		} catch { /* keep current */ }
	}

	async function loadMe() {
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/me`, { headers: authHeaders() });
			if (res.ok) me = await res.json();
		} catch { /* keep current */ }
	}

	async function loadStatus() {
		if (!isFinalsMode) return;
		try {
			const url = `${API_BASE}/api/v1/arena/me/status${latestTick > 0 ? `?since=${latestTick}` : ''}`;
			const res = await fetch(url, { headers: authHeaders() });
			if (!res.ok) return;
			const d = await res.json();
			const incoming: ServiceStatus[] = d.services ?? [];
			if (!incoming.length) return;

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
			services = [...svcMap.values()];

			const allTickNums = services.flatMap(s => s.stores.flatMap(st => st.ticks.map(t => t.tick)));
			if (allTickNums.length) latestTick = Math.max(...allTickNums, latestTick);
		} catch { /* keep current */ }
	}

	async function loadAlerts() {
		if (!isFinalsMode) return;
		try {
			const res = await fetch(`${API_BASE}/api/v1/arena/me/alerts`, { headers: authHeaders() });
			if (res.ok) { const d = await res.json(); alerts = d.alerts ?? alerts; }
		} catch { /* keep current */ }
	}

	async function submitFlags() {
		if (!submitFlag.trim() || submitBusy) return;
		submitBusy = true;
		submitResult = '';
		try {
			const flags = submitFlag.trim().split(/[\s,]+/).filter(Boolean);
			const res = await fetch(`${API_BASE}/api/v1/arena/flags`, {
				method: 'PUT',
				headers: { ...authHeaders(), 'Content-Type': 'application/json' },
				body: JSON.stringify(flags) // bare array per API spec
			});
			if (res.status === 401) { submitResult = '401: check your team token'; return; }
			if (res.status === 400) { submitResult = '400: bad request'; return; }
			const d: Array<{ flag: string; status: string; msg: string }> = await res.json();
			submitResult = d.map(r => `${r.flag.slice(-8)}: ${r.status} (${r.msg})`).join('\n');
			if (res.ok && d.every(r => r.status === 'accepted' || r.status === 'duplicate')) submitFlag = '';
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
			case 'OK': return 'OK';
			case 'RECOVERING': return 'RECOVERING';
			case 'MUMBLE': case 'FAULTY': return 'MUMBLE';
			case 'OFFLINE': case 'DOWN': return 'OFFLINE';
			case 'FLAG_NOT_FOUND': return 'FLAG NOT FOUND';
			case 'ORG_FAULT': return 'ORG FAULT (credited)';
			default: return s;
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

	function fateGlyph(fate: FlagFate | undefined): { text: string; cls: string } {
		if (!fate) return { text: '', cls: '' };
		if (fate === 'held') return { text: '●', cls: 'text-[#9bcea8]' };
		if (fate === 'tide') return { text: 'T', cls: 'text-amber-400' };
		if (fate === 'unreadable') return { text: '?', cls: 'text-stone-500' };
		if (fate === 'stolen_1') return { text: 'x1', cls: 'text-[#e08a85]' };
		if (fate === 'stolen_2_3') return { text: 'x2', cls: 'text-[#e08a85]' };
		if (fate === 'stolen_4_7') return { text: 'x5', cls: 'text-[#e08a85]' };
		return { text: 'x8', cls: 'text-[#e08a85]' };
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

	onMount(() => {
		clockInterval = setInterval(() => { now = Date.now(); }, 1000);
		pollInterval = setInterval(() => {
			loadTick();
			loadStatus();
			loadAlerts();
		}, 30000);
		loadTick();
		loadMe();
		loadStatus();
		loadAlerts();
	});
	onDestroy(() => {
		clearInterval(clockInterval);
		clearInterval(pollInterval);
	});
</script>

<svelte:head><title>Team Status - Anvil</title></svelte:head>

{#if !isFinalsMode}
	<script>window.location.replace('/team');</script>
{:else}

<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-6">
	<!-- tick clock header -->
	<div class="mb-5 flex flex-wrap items-center gap-3">
		<PageHeader title="Team Status" compact />
		<div class="ml-auto flex flex-wrap items-center gap-2">
			{#if me.team_number}
				<span class="rounded-md border border-stone-800 bg-stone-900/60 px-2.5 py-1.5 text-xs text-stone-400">
					Team #{me.team_number} &mdash; {me.vulnbox_ip ?? 'no vulnbox'}
				</span>
			{/if}
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5 text-sm">
				<span class="metadata-label text-stone-500">Tick</span>
				<span class="font-semibold tabular-nums text-stone-100">{tickInfo.tick}</span>
			</div>
			<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-900/60 px-3 py-1.5 text-sm">
				<span class="metadata-label text-stone-500">Ends in</span>
				<span class="font-mono tabular-nums text-stone-100">{fmtCountdown}</span>
			</div>
			{#if tickInfo.phase === 'paused'}
				<div class="flex items-center gap-1.5 rounded-md border border-[#9c7a30]/40 bg-[#9c7a30]/10 px-3 py-1.5 text-sm text-[#e0be72]">
					<Icon icon="mdi:pause-circle-outline" class="h-4 w-4" />
					<span class="font-medium">PAUSED</span>
				</div>
			{:else if tickInfo.phase === 'over'}
				<div class="flex items-center gap-1.5 rounded-md border border-stone-700 bg-stone-900 px-3 py-1.5 text-sm text-stone-500">
					<Icon icon="mdi:flag-checkered" class="h-4 w-4" />
					<span>EVENT OVER</span>
				</div>
			{/if}
		</div>
	</div>

	<!-- exploitation alerts -->
	{#if alerts.length}
		<div class="mb-5 rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden">
			<div class="px-4 py-2.5 border-b border-stone-800 flex items-center gap-2">
				<Icon icon="mdi:bell-ring-outline" class="h-4 w-4 text-stone-500" />
				<span class="text-sm font-medium text-stone-300">Alerts</span>
				<span class="ml-auto metadata-label text-stone-600">1 tick late, bucketed</span>
			</div>
			<ul class="divide-y divide-stone-800/60">
				{#each alerts.slice(0, 10) as a}
					{@const svcName = svcNames.get(a.service_id) ?? a.service_id}
					<li class="flex items-center gap-3 px-4 py-2.5 text-sm">
						{#if a.kind === 'shield_up'}
							<Icon icon="mdi:shield-check" class="h-3.5 w-3.5 shrink-0 text-[#9bcea8]" />
							<span class="text-[#9bcea8] font-medium text-xs">SHIELD UP</span>
						{:else if a.kind === 'holding'}
							<Icon icon="mdi:lock-check-outline" class="h-3.5 w-3.5 shrink-0 text-stone-400" />
							<span class="text-stone-400 font-medium text-xs">HOLDING</span>
						{:else}
							<span class="w-1.5 h-1.5 rounded-full shrink-0 bg-[#b0453a]"></span>
							<span class="text-[#e08a85] font-medium text-xs">EXPLOIT</span>
						{/if}
						<span class="text-stone-400 min-w-0 flex-1 truncate text-xs">{svcName}</span>
						{#if a.bucket !== null}
							<span class="text-xs text-stone-500">{bucketLabel(a.bucket)}</span>
						{/if}
						<span class="text-xs text-stone-600 tabular-nums">t{a.tick}</span>
						<span class="text-xs text-stone-700">{alertAge(a.created_at)}</span>
					</li>
				{/each}
			</ul>
		</div>
	{/if}

	<!-- service timelines: services[]{stores[]{ticks[]}} -->
	{#each services as svc (svc.service_id)}
		{@const latestT = svcLatestTick(svc)}
		{@const svcStatus = latestT?.status ?? 'OK'}
		<div class="mb-5 rounded-lg border border-stone-800 bg-stone-900/40 overflow-hidden">
			<div class="flex flex-wrap items-center gap-3 px-4 py-3 border-b border-stone-800">
				<span class="text-[0.95rem] font-semibold text-stone-200">{svc.name}</span>
				<span class="rounded px-1.5 py-0.5 text-xs font-medium {statusChipKey(svcStatus)}">{statusLabel(svcStatus)}</span>
				<span class="text-xs text-stone-600">{(svcUptime(svc) * 100).toFixed(1)}% up</span>
				{#if latestT?.message}
					<span class="text-xs text-stone-500 truncate max-w-xs" title={latestT.message}>{latestT.message}</span>
				{/if}
			</div>
			<div class="px-4 py-3">
				{#each svc.stores as store (store.store_index)}
					{@const fate = store.ticks.at(-1)?.flag_fate}
					{@const g = fateGlyph(fate)}
					<div class="mb-2 flex items-center gap-2">
						<span class="metadata-label text-stone-500 text-xs w-20 shrink-0 truncate" title={store.store_name}>{store.store_name}</span>
						<div class="flex gap-px overflow-x-auto pb-0.5">
							{#each store.ticks as t}
								{@const credited = !!t.credited_reason}
								<div
									class="relative h-5 w-5 shrink-0 rounded-sm {statusColorKey(t.status)} {credited ? 'ring-1 ring-amber-400/50' : ''}"
									title="{statusLabel(t.status)} t{t.tick}{t.credited_reason ? ' (credited: ' + t.credited_reason + ')' : ''}{t.message ? ' - ' + t.message : ''}"
								>
									{#if credited}
										<span class="absolute top-0 right-0 h-1 w-1 rounded-full bg-amber-400"></span>
									{/if}
								</div>
							{/each}
							{#if store.ticks.length === 0}
								<span class="text-xs text-stone-600 italic">no data yet</span>
							{/if}
						</div>
						{#if g.text}
							<span class="text-[0.8rem] {g.cls} shrink-0" title={fate ?? ''}>{g.text}</span>
						{/if}
						<span class="text-xs tabular-nums text-[#9bcea8] shrink-0">+{fmt1(store.hold)} HOLD</span>
					</div>
				{/each}
			</div>
		</div>
	{/each}

	<!-- legend -->
	<div class="mb-5 flex flex-wrap gap-x-4 gap-y-1.5 text-[0.7rem] text-stone-600">
		{#each [['OK','bg-[#4b7355]'],['RECOVERING','bg-[#3f6a86]'],['MUMBLE','bg-[#9c7a30]'],['OFFLINE','bg-[#b0453a]'],['FLAG NOT FOUND','bg-[#7a4a86]'],['ORG FAULT','bg-stone-700']] as [lbl, cls]}
			<span class="inline-flex items-center gap-1"><span class="h-2.5 w-2.5 rounded-sm {cls}"></span>{lbl}</span>
		{/each}
		<span class="ml-4 inline-flex items-center gap-1 text-[#9bcea8]">● held</span>
		<span class="inline-flex items-center gap-1 text-[#e08a85]">x2 stolen</span>
		<span class="inline-flex items-center gap-1 text-amber-400">T tide</span>
		<span class="inline-flex items-center gap-1 text-stone-500">? unreadable</span>
		<span class="ml-4 inline-flex items-center gap-1">
			<span class="relative h-2.5 w-2.5 rounded-sm bg-stone-700"><span class="absolute top-0 right-0 h-1 w-1 rounded-full bg-amber-400"></span></span>
			<span class="text-amber-400/70">credited</span>
		</span>
	</div>

	<div class="grid gap-4 sm:grid-cols-2">
		<!-- flag submission -->
		<Card title="Submit flags">
			<form on:submit|preventDefault={submitFlags} class="flex flex-col gap-2">
				<textarea
					bind:value={submitFlag}
					rows="3"
					placeholder="H7CTF&#123;...&#125; one per line"
					class="w-full resize-none rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2 text-sm font-mono text-stone-200 placeholder-stone-600 focus:border-stone-600 focus:outline-none transition-colors"
					autocomplete="off" spellcheck="false"
				></textarea>
				<button
					type="submit"
					disabled={submitBusy || !submitFlag.trim() || tickInfo.phase !== 'live'}
					class="rounded-md bg-stone-100 text-stone-950 font-medium py-2 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{submitBusy ? 'Submitting...' : tickInfo.phase !== 'live' ? 'Game not live' : 'Submit'}
				</button>
				{#if submitResult}
					<pre class="text-xs font-mono text-stone-400 whitespace-pre-wrap break-all">{submitResult}</pre>
				{/if}
			</form>
		</Card>

		<!-- team info -->
		<Card title="Your network">
			<div class="space-y-3 text-sm">
				<div class="flex justify-between text-stone-500">
					<span>Vulnbox</span>
					<span class="font-mono text-stone-200">{me.vulnbox_ip ?? 'not assigned'}</span>
				</div>
				<div class="flex justify-between text-stone-500">
					<span>Team #</span>
					<span class="text-stone-200">{me.team_number ?? '-'}</span>
				</div>
				<div class="flex justify-between text-stone-500">
					<span>Submit token</span>
					<span class="font-mono text-stone-500 text-xs">{me.token}</span>
				</div>
				<div class="flex gap-2 pt-1">
					{#if me.vulnbox_ip}
						<a
							href="ssh://root@{me.vulnbox_ip}"
							class="flex-1 rounded-md border border-stone-800 bg-stone-900/60 py-2 text-xs text-center text-stone-300 hover:bg-stone-800/60 transition-colors"
						>
							SSH vulnbox
						</a>
					{/if}
					<a
						href="{API_BASE}/api/v1/arena/attack-info"
						class="flex-1 rounded-md border border-stone-800 bg-stone-900/60 py-2 text-xs text-center text-stone-300 hover:bg-stone-800/60 transition-colors"
						target="_blank" rel="noreferrer"
					>
						Attack info
					</a>
				</div>
				<p class="text-xs text-stone-600">Vulnbox reboot/reset: 501 until OCI control plane is wired.</p>
			</div>
		</Card>
	</div>

	<!-- ORG_FAULT rule -->
	<details class="mt-5 rounded-lg border border-stone-800 bg-stone-900/30 overflow-hidden">
		<summary class="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm text-stone-400 hover:text-stone-300 transition-colors select-none">
			<Icon icon="mdi:information-outline" class="h-4 w-4 shrink-0 text-stone-600" />
			<span class="font-medium">Our faults rule</span>
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
