<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import {
		api,
		type ArenaCheckerTask,
		type ArenaHill,
		type ArenaService,
		type ArenaSuspiciousSLA,
		type ArenaTeam,
		type ArenaTick,
		type ReadinessReport
	} from '$api';
	import { confirmDialog, alertDialog } from '$lib/stores/dialog';
	import { platformInfo, refreshPlatformInfo } from '$lib/stores/platform';

	let loading = true;
	let refreshing = false;
	let error = '';
	let busy = '';
	let now = Date.now();
	let refreshedAt = '';
	let tick: ArenaTick | null = null;
	let readiness: ReadinessReport | null = null;
	let teams: ArenaTeam[] = [];
	let services: ArenaService[] = [];
	let hills: ArenaHill[] = [];
	let teamIPs: Record<string, string> = {};
	let serviceEnabled: Record<string, boolean> = {};
	let rotatedToken = '';
	let rotatedTeam = '';
	let extendMinutes = 15;
	let extensionResult: { end_at?: string } | null = null;
	let credit = { service: '', team_id: '', tick_from: 1, tick_to: 1, reason: '' };
	let checker = { team_id: '', service_id: '', tick: 1 };
	let checkerTasks: ArenaCheckerTask[] = [];
	let checkerLoaded = false;
	let suspiciousSince = 0;
	let suspiciousRows: ArenaSuspiciousSLA[] = [];
	let clock: ReturnType<typeof setInterval> | undefined;

	const field = 'rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-200 placeholder-stone-600 outline-none transition-colors focus:border-stone-500 disabled:opacity-50';
	const primary = 'inline-flex items-center justify-center gap-2 rounded-md bg-amber-500/90 px-3.5 py-2 text-sm font-medium leading-none text-stone-950 transition-colors hover:bg-amber-500 disabled:cursor-not-allowed disabled:opacity-50';
	const ghost = 'inline-flex items-center justify-center gap-2 rounded-md border border-stone-700 px-3.5 py-2 text-sm font-medium leading-none text-stone-300 transition-colors hover:bg-stone-800/40 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-50';
	const destructive = `${ghost} border-down/40 text-down`;

	function message(e: unknown, fallback: string) {
		return e instanceof Error ? e.message : fallback;
	}

	function showIST(value?: string) {
		if (!value) return 'Not set';
		return new Intl.DateTimeFormat('en-IN', {
			timeZone: 'Asia/Kolkata', dateStyle: 'medium', timeStyle: 'medium'
		}).format(new Date(value)) + ' IST';
	}

	function timeToNextTick() {
		if (!tick || tick.tick === 0 || !tick.ends_at || tick.ends_at.startsWith('0001-')) return 'Waiting for first tick';
		const seconds = Math.max(0, Math.ceil((Date.parse(tick.ends_at) - now) / 1000));
		return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
	}

	function hydrateDrafts() {
		teamIPs = Object.fromEntries(teams.map((team) => [team.id, team.vulnbox_ip ?? '']));
		serviceEnabled = Object.fromEntries(services.map((service) => [service.id, service.enabled]));
		if (!credit.service && services[0]) credit.service = services[0].slug;
		if (!checker.team_id && teams[0]) checker.team_id = teams[0].id;
		if (!checker.service_id && services[0]) checker.service_id = services[0].id;
		if (tick && checker.tick === 1) checker.tick = Math.max(1, tick.tick);
	}

	async function load() {
		refreshing = true;
		error = '';
		try {
			const [tickResult, readinessResult, teamResult, serviceResult, hillResult] = await Promise.all([
				api.getArenaTick(), api.getReadiness(), api.getArenaTeams(), api.getArenaServices(), api.getArenaHills()
			]);
			tick = tickResult;
			readiness = readinessResult;
			teams = teamResult.teams;
			services = serviceResult.services;
			hills = hillResult.hills;
			hydrateDrafts();
			refreshedAt = showIST(new Date().toISOString());
		} catch (e) {
			error = message(e, 'Arena operations could not be loaded');
		} finally {
			loading = false;
			refreshing = false;
		}
	}

	async function setState(state: 'running' | 'paused' | 'stopped') {
		const verb = state === 'running' ? 'Start or resume' : state === 'paused' ? 'Pause' : 'Stop';
		if (!(await confirmDialog({
			title: `${verb} finals`, message: `${verb} the attack-defense engine now?`,
			confirmLabel: verb, danger: state === 'stopped'
		}))) return;
		busy = `state-${state}`;
		try {
			await api.setArenaState(state);
			await refreshPlatformInfo();
			await load();
		} catch (e) {
			await alertDialog({ title: 'Game state unchanged', message: message(e, 'Failed to update game state') });
		} finally {
			busy = '';
		}
	}

	async function extendGame() {
		if (!(await confirmDialog({
			title: 'Extend finals clock',
			message: `Shift the event end, freeze, future service releases, advisories, Hill windows, and the active Hill round by ${extendMinutes} minutes?`,
			confirmLabel: 'Extend clock'
		}))) return;
		busy = 'extend';
		try {
			await api.extendArena(extendMinutes);
			const info = await api.getPlatformInfo();
			extensionResult = { end_at: info.event?.end_at };
			await refreshPlatformInfo();
			await load();
		} catch (e) {
			await alertDialog({ title: 'Clock unchanged', message: message(e, 'Failed to extend finals') });
		} finally {
			busy = '';
		}
	}

	async function saveTeamIP(team: ArenaTeam) {
		const value = teamIPs[team.id]?.trim() ?? '';
		if (!(await confirmDialog({
			title: 'Change vulnbox address', message: `Set ${team.name} to ${value || 'an empty address'}?`, confirmLabel: 'Update address'
		}))) return;
		busy = `team-ip-${team.id}`;
		try {
			await api.updateArenaTeamVulnboxIP(team.id, value);
			await load();
		} catch (e) {
			await alertDialog({ title: 'Address unchanged', message: message(e, 'Failed to update vulnbox address') });
		} finally {
			busy = '';
		}
	}

	async function rotateToken(team: ArenaTeam) {
		if (!(await confirmDialog({
			title: 'Rotate submit token', message: `Invalidate the current submit token for ${team.name}?`,
			confirmLabel: 'Rotate token', danger: true
		}))) return;
		busy = `team-token-${team.id}`;
		try {
			const result = await api.rotateArenaTeamToken(team.id);
			rotatedTeam = team.name;
			rotatedToken = result.token;
			await load();
		} catch (e) {
			await alertDialog({ title: 'Token unchanged', message: message(e, 'Failed to rotate token') });
		} finally {
			busy = '';
		}
	}

	async function copyToken() {
		let copied = false;
		try {
			await navigator.clipboard.writeText(rotatedToken);
			copied = true;
		} catch {
			const field = document.createElement('textarea');
			field.value = rotatedToken;
			field.style.position = 'fixed';
			field.style.opacity = '0';
			document.body.appendChild(field);
			field.select();
			copied = document.execCommand('copy');
			field.remove();
		}
		await alertDialog(copied
			? { title: 'Copied', message: 'The new token is on your clipboard.' }
			: { title: 'Copy unavailable', message: 'Select the token and copy it before closing this dialog.' });
	}

	function closeToken() {
		rotatedToken = '';
		rotatedTeam = '';
	}

	async function saveService(service: ArenaService) {
		if (!(await confirmDialog({
			title: 'Update service state',
			message: `${serviceEnabled[service.id] ? 'Enable' : 'Disable'} ${service.name}?`,
			confirmLabel: 'Update service'
		}))) return;
		busy = `service-${service.id}`;
		try {
			await api.updateArenaService(service.id, { enabled: serviceEnabled[service.id] });
			await load();
		} catch (e) {
			await alertDialog({ title: 'Service unchanged', message: message(e, 'Failed to update service') });
		} finally {
			busy = '';
		}
	}

	async function submitCredit() {
		const service = services.find((item) => item.slug === credit.service);
		const team = teams.find((item) => item.id === credit.team_id);
		if (!(await confirmDialog({
			title: 'Apply SLA credit',
			message: `Credit ${team?.name ?? 'every team'} for ${service?.name ?? credit.service}, ticks ${credit.tick_from} through ${credit.tick_to}? Reason: ${credit.reason}`,
			confirmLabel: 'Apply credit'
		}))) return;
		busy = 'credit';
		try {
			await api.createArenaCredit(credit);
			credit = { ...credit, reason: '' };
			await alertDialog({ title: 'Credit recorded', message: 'The credit will apply on the next score recompute.' });
		} catch (e) {
			await alertDialog({ title: 'Credit not recorded', message: message(e, 'Failed to apply SLA credit') });
		} finally {
			busy = '';
		}
	}

	async function resettle() {
		if (!(await confirmDialog({
			title: 'Resettle finished game', message: 'Recompute the finished game from raw rows and all organizer credits?',
			confirmLabel: 'Resettle', danger: true
		}))) return;
		busy = 'resettle';
		try {
			await api.resettleArena();
			await alertDialog({ title: 'Game resettled', message: 'Scores were recomputed from the stored game data.' });
		} catch (e) {
			await alertDialog({ title: 'Resettle failed', message: message(e, 'Failed to resettle game') });
		} finally {
			busy = '';
		}
	}

	async function loadCheckerLog() {
		busy = 'checker-log';
		try {
			checkerTasks = (await api.getArenaCheckerLog(checker.team_id, checker.service_id, checker.tick)).tasks ?? [];
			checkerLoaded = true;
		} catch (e) {
			await alertDialog({ title: 'Checker log unavailable', message: message(e, 'Failed to load checker log') });
		} finally {
			busy = '';
		}
	}

	async function loadSuspicious() {
		busy = 'suspicious';
		try {
			suspiciousRows = (await api.getArenaSuspiciousSLA(suspiciousSince)).rows ?? [];
		} catch (e) {
			await alertDialog({ title: 'SLA report unavailable', message: message(e, 'Failed to load suspicious SLA report') });
		} finally {
			busy = '';
		}
	}

	onMount(() => {
		load();
		loadSuspicious();
		clock = setInterval(() => { now = Date.now(); }, 1000);
	});

	onDestroy(() => {
		if (clock) clearInterval(clock);
		closeToken();
	});
</script>

<div class="space-y-6">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
		<div>
			<h2 class="text-xl font-semibold text-stone-100">Arena operations</h2>
			<p class="mt-1 text-xs text-stone-500">Attack-defense and Adaptive Hill controls. All schedule times use Asia/Kolkata.</p>
		</div>
		<div class="flex items-center gap-3">
			{#if refreshedAt}<span class="text-[11px] text-stone-600">Updated {refreshedAt}</span>{/if}
			<button type="button" class={ghost} disabled={refreshing} on:click={load}>
				<Icon icon="mdi:refresh" class="h-4 w-4 {refreshing ? 'animate-spin' : ''}" /> Refresh
			</button>
		</div>
	</div>

	{#if loading}
		<div class="grid min-h-[30vh] place-items-center"><Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-600" /></div>
	{:else if error}
		<EmptyState icon="mdi:alert-circle-outline" text={error}><button type="button" class="mt-3 text-sm text-amber-500" on:click={load}>Retry</button></EmptyState>
	{:else}
		<section aria-labelledby="arena-readiness">
			<Card bodyClass="p-0">
				<div slot="header" class="flex min-w-0 items-center gap-2">
					<OpticalIcon icon="mdi:clipboard-check-outline" size={15} box={16} className="text-stone-500" />
					<h3 id="arena-readiness" class="text-sm font-semibold text-stone-200">Readiness</h3>
				</div>
				<span slot="meta" class="rounded-full px-2 py-1 text-[11px] font-medium {readiness?.ready ? 'bg-up/10 text-up' : 'bg-down/10 text-down'}">
					{readiness?.ready ? 'Ready' : `${readiness?.blockers ?? 0} blockers`}
				</span>
				<div class="divide-y divide-stone-800/70">
					{#each readiness?.checks ?? [] as check}
						<div class="grid gap-1 px-4 py-3 sm:grid-cols-[9rem_1fr_auto] sm:items-start sm:gap-4">
							<p class="metadata-label text-stone-600">{check.group}</p>
							<div><p class="text-sm text-stone-300">{check.label}</p><p class="mt-1 text-xs {check.status === 'blocker' ? 'text-down' : check.status === 'warning' ? 'text-warn' : 'text-stone-500'}">{check.detail}</p></div>
							<span class="text-[11px] font-medium uppercase tracking-wide {check.status === 'pass' ? 'text-up' : check.status === 'warning' ? 'text-warn' : 'text-down'}">{check.status}</span>
						</div>
					{/each}
				</div>
			</Card>
		</section>

		<section class="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]" aria-labelledby="arena-clock">
			<Card bodyClass="p-4">
				<div slot="header" class="flex items-center gap-2"><OpticalIcon icon="mdi:clock-outline" size={15} box={16} className="text-stone-500" /><h3 id="arena-clock" class="text-sm font-semibold text-stone-200">Game clock</h3></div>
				<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
					<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">State</p><p class="mt-1 text-sm capitalize text-stone-200">{$platformInfo?.game_state ?? 'unknown'}</p></div>
					<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">Phase</p><p class="mt-1 text-sm capitalize text-stone-200">{tick?.phase ?? 'unknown'}</p></div>
					<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">Current tick</p><p class="mt-1 text-xl font-semibold tabular-nums text-stone-100">{tick?.tick ?? 0}</p></div>
					<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">Next tick</p><p class="mt-1 text-xl font-semibold tabular-nums text-amber-500">{timeToNextTick()}</p></div>
				</div>
				<div class="mt-4 flex flex-wrap gap-2">
					<button class={primary} disabled={busy !== '' || $platformInfo?.game_state === 'running'} on:click={() => setState('running')}>Start / Resume</button>
					<button class={ghost} disabled={busy !== '' || $platformInfo?.game_state === 'paused'} on:click={() => setState('paused')}>Pause</button>
					<button class={destructive} disabled={busy !== '' || $platformInfo?.game_state === 'stopped'} on:click={() => setState('stopped')}>Stop</button>
				</div>
				<div class="mt-4 border-t border-stone-800 pt-4 text-xs text-stone-500"><p>Tick opened: {showIST(tick?.started_at)}</p><p class="mt-1">Tick closes: {showIST(tick?.ends_at)}</p></div>
			</Card>

			<Card title="Extend schedule" bodyClass="p-4">
				<p class="text-xs leading-5 text-stone-500">Shifts the event end, freeze, future releases, outbreak slots, Hill windows, and the active Hill round together.</p>
				<div class="mt-4 flex items-end gap-2">
					<label class="min-w-0 flex-1"><span class="metadata-label mb-1.5 block text-stone-500">Minutes</span><input class="w-full {field} tabular-nums" type="number" min="1" max="1440" bind:value={extendMinutes} /></label>
					<button class={primary} disabled={busy !== '' || extendMinutes < 1 || extendMinutes > 1440} on:click={extendGame}>Extend</button>
				</div>
				{#if extensionResult}
					<div class="mt-4 rounded-md border border-up/20 bg-up/5 p-3 text-xs text-stone-400"><p>New end: {showIST(extensionResult.end_at)}</p><p class="mt-1 text-stone-600">Freeze time is not returned by the current admin API.</p></div>
				{/if}
			</Card>
		</section>

		<section aria-labelledby="arena-teams">
			<Card bodyClass="p-0">
				<div slot="header" class="flex items-center gap-2"><OpticalIcon icon="mdi:account-group-outline" size={15} box={16} className="text-stone-500" /><h3 id="arena-teams" class="text-sm font-semibold text-stone-200">Teams</h3></div>
				<span slot="meta" class="text-xs tabular-nums text-stone-500">{teams.length} including NOP</span>
				<div class="overflow-x-auto">
					<table class="w-full min-w-[760px] text-sm">
						<thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-4 py-2.5 text-left">Team</th><th class="px-4 py-2.5 text-left">Status</th><th class="px-4 py-2.5 text-left">Vulnbox IP</th><th class="px-4 py-2.5 text-right">Actions</th></tr></thead>
						<tbody>
							{#each teams as team}
								<tr class="border-b border-stone-800/60">
									<td class="px-4 py-3"><p class="text-stone-200">{team.name}{#if team.is_nop}<span class="ml-2 rounded-full bg-indigo-400/10 px-2 py-0.5 text-[10px] text-indigo-300">NOP</span>{/if}</p><p class="mt-0.5 text-[11px] text-stone-600">{team.slug}</p></td>
									<td class="px-4 py-3"><span class={team.status === 'active' ? 'text-up' : 'text-down'}>{team.status}</span></td>
									<td class="px-4 py-3"><input aria-label={`Vulnbox IP for ${team.name}`} class="w-40 {field} font-mono text-xs" value={teamIPs[team.id]} on:input={(event) => { teamIPs[team.id] = event.currentTarget.value; teamIPs = { ...teamIPs }; }} /></td>
									<td class="px-4 py-3"><div class="flex justify-end gap-2"><button class={ghost} disabled={busy !== ''} on:click={() => saveTeamIP(team)}>Save IP</button><button class={destructive} disabled={busy !== ''} on:click={() => rotateToken(team)}>Rotate token</button></div></td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<div class="border-t border-stone-800 bg-stone-950/40 px-4 py-3 text-xs text-stone-600">Team number, member count, and token prefix are not returned by the current admin teams API.</div>
			</Card>
		</section>

		<section aria-labelledby="arena-services">
			<Card bodyClass="p-0">
				<div slot="header" class="flex items-center gap-2"><OpticalIcon icon="mdi:server-outline" size={15} box={16} className="text-stone-500" /><h3 id="arena-services" class="text-sm font-semibold text-stone-200">Services</h3></div>
				<span slot="meta" class="text-xs tabular-nums text-stone-500">{services.length} configured</span>
				<div class="divide-y divide-stone-800/70">
					{#each services as service}
						<div class="p-4">
							<div class="grid gap-3 lg:grid-cols-[minmax(12rem,1fr)_9rem_8rem_auto] lg:items-end">
								<div><div class="flex items-center gap-2"><p class="text-sm font-medium text-stone-200">{service.name}</p><span class="rounded-full px-2 py-0.5 text-[10px] {service.enabled ? 'bg-up/10 text-up' : 'bg-stone-800 text-stone-500'}">{service.enabled ? 'Enabled' : 'Disabled'}</span></div><p class="mt-1 text-xs text-stone-600">{service.slug} · {service.category}</p></div>
								<div><p class="metadata-label mb-1.5 text-stone-500">Port</p><p class="rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm tabular-nums text-stone-300">{service.port ?? '-'}</p></div>
								<label><span class="metadata-label mb-1.5 block text-stone-500">Enabled</span><select class="w-full {field}" value={String(serviceEnabled[service.id])} on:change={(event) => { serviceEnabled[service.id] = event.currentTarget.value === 'true'; serviceEnabled = { ...serviceEnabled }; }}><option value="true">Yes</option><option value="false">No</option></select></label>
								<button class={primary} disabled={busy !== '' || serviceEnabled[service.id] === service.enabled} on:click={() => saveService(service)}>Save state</button>
							</div>
						</div>
					{/each}
				</div>
				<div class="border-t border-stone-800 bg-stone-950/40 px-4 py-3 text-xs text-stone-600">Release time and outbreak fields are not returned by the current admin services API, so those editors are withheld.</div>
			</Card>
		</section>

		<section aria-labelledby="arena-hills">
			<div class="mb-3 flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between"><div><h3 id="arena-hills" class="text-sm font-semibold text-stone-200">Adaptive Hills</h3><p class="mt-1 text-xs text-stone-600">Scheduled in IST. The 00:00 to 06:00 Night Watch band blocks new Hill openings.</p></div><span class="rounded-full border border-indigo-400/20 bg-indigo-400/5 px-2.5 py-1 text-[11px] text-indigo-300">Night Watch 00:00 to 06:00 IST</span></div>
			<div class="grid gap-4 xl:grid-cols-3">
				{#each hills as hill}
					<Card bodyClass="p-4">
						<div slot="header" class="min-w-0"><div class="flex items-center gap-2"><h4 class="truncate text-sm font-semibold text-stone-200">{hill.name}</h4><span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] {hill.enabled ? 'bg-up/10 text-up' : 'bg-stone-800 text-stone-500'}">{hill.enabled ? 'Open' : 'Closed'}</span></div><p class="mt-1 text-[11px] text-stone-600">{hill.protocol}</p></div>
						<div class="space-y-2 text-xs text-stone-500"><p>Controller: {hill.base_url ?? 'Not returned'}</p><p>Reset interval: {hill.reset_seconds ?? 0} seconds</p><p class="text-stone-600">Schedule windows are not returned by the current admin Hills API, so the replacement editor is withheld.</p></div>
					</Card>
				{/each}
			</div>
		</section>

		<section aria-labelledby="arena-fairness">
			<div class="mb-3"><h3 id="arena-fairness" class="text-sm font-semibold text-stone-200">Fairness desk</h3><p class="mt-1 text-xs text-stone-600">Organizer credits and read-only checker evidence.</p></div>
			<div class="grid gap-6 xl:grid-cols-2">
				<Card title="SLA credit" bodyClass="p-4">
					<div class="grid gap-3 sm:grid-cols-2">
						<label><span class="metadata-label mb-1.5 block text-stone-500">Service</span><select class="w-full {field}" bind:value={credit.service}>{#each services as service}<option value={service.slug}>{service.name}</option>{/each}</select></label>
						<label><span class="metadata-label mb-1.5 block text-stone-500">Team</span><select class="w-full {field}" bind:value={credit.team_id}><option value="">Every team</option>{#each teams as team}<option value={team.id}>{team.name}</option>{/each}</select></label>
						<label><span class="metadata-label mb-1.5 block text-stone-500">From tick</span><input class="w-full {field}" type="number" min="1" bind:value={credit.tick_from} /></label>
						<label><span class="metadata-label mb-1.5 block text-stone-500">To tick</span><input class="w-full {field}" type="number" min={credit.tick_from} bind:value={credit.tick_to} /></label>
					</div>
					<label class="mt-3 block"><span class="metadata-label mb-1.5 block text-stone-500">Reason</span><input class="w-full {field}" maxlength="200" placeholder="Organizer incident reference" bind:value={credit.reason} /></label>
					<div class="mt-4 flex flex-wrap gap-2"><button class={primary} disabled={busy !== '' || !credit.reason.trim() || credit.tick_from < 1 || credit.tick_to < credit.tick_from} on:click={submitCredit}>Apply credit</button><button class={destructive} disabled={busy !== ''} on:click={resettle}>Resettle finished game</button></div>
				</Card>

				<Card title="Suspicious SLA" bodyClass="p-0">
					<div class="flex items-end gap-2 border-b border-stone-800 p-4"><label class="min-w-0 flex-1"><span class="metadata-label mb-1.5 block text-stone-500">Since tick</span><input class="w-full {field}" type="number" min="0" bind:value={suspiciousSince} /></label><button class={ghost} disabled={busy !== ''} on:click={loadSuspicious}>Refresh report</button></div>
					{#if suspiciousRows.length === 0}<EmptyState icon="mdi:shield-check-outline" text="No suspicious SLA rows in this range." />{:else}<div class="max-h-80 overflow-auto divide-y divide-stone-800/70">{#each suspiciousRows as row}<div class="flex items-center justify-between gap-3 px-4 py-3"><div><p class="text-sm text-stone-300">{row.team_name} · {row.service_name}</p><p class="mt-1 text-xs text-stone-600">Tick {row.tick}: {row.down_count} of {row.total_in_tick} down or faulty</p></div><span class="text-sm font-medium tabular-nums text-down">{Math.round(row.down_rate * 100)}%</span></div>{/each}</div>{/if}
				</Card>
			</div>

			<Card title="Checker log" bodyClass="p-0">
				<div class="grid gap-3 border-b border-stone-800 p-4 sm:grid-cols-[1fr_1fr_8rem_auto] sm:items-end">
					<label><span class="metadata-label mb-1.5 block text-stone-500">Team</span><select class="w-full {field}" bind:value={checker.team_id}>{#each teams as team}<option value={team.id}>{team.name}</option>{/each}</select></label>
					<label><span class="metadata-label mb-1.5 block text-stone-500">Service</span><select class="w-full {field}" bind:value={checker.service_id}>{#each services as service}<option value={service.id}>{service.name}</option>{/each}</select></label>
					<label><span class="metadata-label mb-1.5 block text-stone-500">Tick</span><input class="w-full {field}" type="number" min="1" bind:value={checker.tick} /></label>
					<button class={ghost} disabled={busy !== ''} on:click={loadCheckerLog}>Load log</button>
				</div>
				{#if !checkerLoaded}<p class="p-4 text-xs text-stone-600">Choose a team, service, and tick to inspect checker tasks.</p>{:else if checkerTasks.length === 0}<EmptyState icon="mdi:file-search-outline" text="No checker tasks match this query." />{:else}<div class="overflow-x-auto"><table class="w-full min-w-[760px] text-sm"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-4 py-2.5 text-left">Method</th><th class="px-4 py-2.5 text-right">Variant</th><th class="px-4 py-2.5 text-left">Status</th><th class="px-4 py-2.5 text-left">Code</th><th class="px-4 py-2.5 text-left">Message</th><th class="px-4 py-2.5 text-right">Latency</th></tr></thead><tbody>{#each checkerTasks as task}<tr class="border-b border-stone-800/60"><td class="px-4 py-3 text-stone-300">{task.method}</td><td class="px-4 py-3 text-right tabular-nums text-stone-500">{task.variant}</td><td class="px-4 py-3 {task.status === 'OK' ? 'text-up' : 'text-down'}">{task.status ?? 'pending'}</td><td class="px-4 py-3 font-mono text-xs text-stone-500">{task.code ?? '-'}</td><td class="max-w-md px-4 py-3 text-xs text-stone-400">{task.message ?? '-'}</td><td class="px-4 py-3 text-right tabular-nums text-stone-500">{task.latency_ms == null ? '-' : `${task.latency_ms} ms`}</td></tr>{/each}</tbody></table></div>{/if}
			</Card>
		</section>
	{/if}
</div>

{#if rotatedToken}
	<div class="fixed inset-0 z-50 grid place-items-center p-4">
		<button type="button" class="absolute inset-0 bg-black/75" aria-label="Close token dialog" on:click={closeToken}></button>
		<div class="relative w-full max-w-lg rounded-lg border border-stone-700 bg-stone-950 p-5 shadow-2xl" role="dialog" aria-modal="true" aria-labelledby="rotated-token-title" tabindex="-1">
			<h3 id="rotated-token-title" class="text-base font-semibold text-stone-100">New token for {rotatedTeam}</h3>
			<p class="mt-2 text-xs leading-5 text-warn">This full token is shown once. Copy it before closing this dialog.</p>
			<code class="mt-4 block break-all rounded-md border border-stone-800 bg-black p-3 text-sm text-stone-200">{rotatedToken}</code>
			<div class="mt-5 flex justify-end gap-2"><button class={ghost} on:click={copyToken}><Icon icon="mdi:content-copy" class="h-4 w-4" />Copy</button><button class={primary} on:click={closeToken}>Done</button></div>
		</div>
	</div>
{/if}
