<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { formatLocalDateTimeWithZone, instantTitle } from '$lib/time';
	import { alertDialog, confirmDialog, promptDialog } from '$lib/stores/dialog';

	let loading = true;
	let error = '';
	let events: any[] = [];
	let instanceFlags: any[] = [];
	let statusFilter = '';
	let selected: any = null;
	let reviewStatus: 'open' | 'reviewing' | 'confirmed' | 'dismissed' = 'open';
	let reviewNote = '';
	let reviewSaving = false;
	let accountSaving = false;
	let warningSaving = false;
	let reviewError = '';
	let showInstanceFlags = false;

	$: observedIPs = new Set(events.map((event) => event.submitter_ip).filter(Boolean)).size;
	$: involvedAccounts = new Set(events.flatMap((event) => [event.owner_user_id, event.submitter_user_id]).filter(Boolean)).size;
	$: openCases = events.filter((event) => event.review_status === 'open' || event.review_status === 'reviewing').length;

	onMount(load);

	async function load() {
		loading = true;
		error = '';
		try {
			const params: Record<string, string> = {};
			if (statusFilter) params.status = statusFilter;
			const [sharesRes, flagsRes] = await Promise.all([
				api.getFlagShares(params),
				api.getInstanceFlags()
			]);
			events = sharesRes.flag_shares || [];
			instanceFlags = flagsRes.instance_flags || [];
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Audit data could not be loaded';
		} finally {
			loading = false;
		}
	}

	function openEvent(event: any) {
		selected = event;
		reviewStatus = event.review_status || 'open';
		reviewNote = event.review_note || '';
		reviewError = '';
	}

	function closeEvent() {
		if (reviewSaving) return;
		selected = null;
		reviewError = '';
	}

	async function saveReview() {
		if (!selected) return;
		reviewSaving = true;
		reviewError = '';
		try {
			await api.reviewFlagShare(selected.id, reviewStatus, reviewNote);
			const updated = {
				...selected,
				review_status: reviewStatus,
				review_note: reviewNote || null,
				reviewed_at: reviewStatus === 'open' ? null : Math.floor(Date.now() / 1000)
			};
			events = events.map((event) => event.id === selected.id ? updated : event);
			selected = updated;
		} catch (cause) {
			reviewError = cause instanceof Error ? cause.message : 'Review could not be saved';
		} finally {
			reviewSaving = false;
		}
	}

	async function toggleSubmitterBan() {
		if (!selected?.submitter_user_id) return;
		const banned = selected.submitter_status === 'banned';
		const confirmed = await confirmDialog({
			title: banned ? 'Restore account access' : 'Ban submitting account',
			message: banned
				? `Unban ${selected.submitter_username}?`
				: `Ban ${selected.submitter_username}? Their existing access will stop immediately. The evidence case remains separate and unchanged.`,
			confirmLabel: banned ? 'Unban' : 'Ban account',
			danger: !banned
		});
		if (!confirmed) return;
		accountSaving = true;
		reviewError = '';
		try {
			await (banned ? api.unbanUser(selected.submitter_user_id) : api.banUser(selected.submitter_user_id));
			const updated = { ...selected, submitter_status: banned ? 'active' : 'banned' };
			events = events.map((event) => event.id === selected.id ? updated : event);
			selected = updated;
		} catch (cause) {
			reviewError = cause instanceof Error ? cause.message : 'Account status could not be changed';
		} finally {
			accountSaving = false;
		}
	}

	async function warnSubmitter() {
		if (!selected?.submitter_user_id) return;
		const message = await promptDialog({
			title: `Warn ${selected.submitter_username}`,
			message: 'The participant receives a pinned private organizer notification. Keep the wording factual and identify the policy involved.',
			placeholder: 'Explain the concern and the action the participant should take.'
		});
		if (message === null || !message.trim()) return;
		warningSaving = true;
		reviewError = '';
		try {
			await api.warnUser(selected.submitter_user_id, message.trim());
			await alertDialog({ title: 'Warning sent', message: `${selected.submitter_username} received the private organizer warning.` });
		} catch (cause) {
			reviewError = cause instanceof Error ? cause.message : 'Warning could not be sent';
		} finally {
			warningSaving = false;
		}
	}

	function statusLabel(status: string) {
		return status === 'reviewing' ? 'In review' : status.charAt(0).toUpperCase() + status.slice(1);
	}

	function statusClass(status: string) {
		if (status === 'confirmed') return 'border-down/30 bg-down/10 text-down';
		if (status === 'dismissed') return 'border-stone-700 bg-stone-800/60 text-stone-400';
		if (status === 'reviewing') return 'border-blue-500/30 bg-blue-500/10 text-blue-300';
		return 'border-amber-500/30 bg-amber-500/10 text-amber-300';
	}

	function locationLabel(event: any) {
		return [event.city, event.region, event.country_code].filter(Boolean).join(', ');
	}

	function deviceLabel(value: string | null | undefined) {
		if (!value) return 'Not captured';
		let browser = 'Other client';
		let system = '';
		if (/Firefox\//i.test(value)) browser = 'Firefox';
		else if (/Edg\//i.test(value)) browser = 'Edge';
		else if (/Chrome\//i.test(value)) browser = 'Chrome';
		else if (/Safari\//i.test(value)) browser = 'Safari';
		else if (/curl\//i.test(value)) browser = 'curl';
		if (/Windows/i.test(value)) system = 'Windows';
		else if (/Android/i.test(value)) system = 'Android';
		else if (/iPhone|iPad/i.test(value)) system = 'iOS';
		else if (/Macintosh|Mac OS/i.test(value)) system = 'macOS';
		else if (/Linux/i.test(value)) system = 'Linux';
		return system ? `${browser} on ${system}` : browser;
	}

	function handleRowKey(event: KeyboardEvent, item: any) {
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			openEvent(item);
		}
	}
</script>

<svelte:window on:keydown={(event) => event.key === 'Escape' && closeEvent()} />

<div class="space-y-6">
	<div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
		<h2 class="text-lg font-semibold text-stone-100">Integrity review</h2>
		<div class="flex flex-wrap items-end gap-2 sm:justify-end">
			<label class="block">
				<span class="mb-1 block text-xs text-stone-500">Status</span>
				<select bind:value={statusFilter} on:change={load} class="h-9 rounded-md border border-stone-800 bg-stone-950 px-3 text-xs text-stone-300 outline-none focus:border-stone-600">
					<option value="">All statuses</option>
					<option value="open">Open</option>
					<option value="reviewing">In review</option>
					<option value="confirmed">Confirmed</option>
					<option value="dismissed">Dismissed</option>
				</select>
			</label>
			<button type="button" on:click={load} disabled={loading} class="flex h-9 items-center gap-1.5 rounded-md border border-stone-800 px-3 text-xs text-stone-400 transition-colors hover:border-stone-700 hover:text-stone-200 disabled:opacity-50">
				<Icon icon="mdi:refresh" class="h-3.5 w-3.5 {loading ? 'animate-spin' : ''}" />
				Refresh
			</button>
		</div>
	</div>

	{#if error}
		<div class="flex items-center justify-between gap-3 rounded-lg border border-down/20 bg-down/10 px-4 py-3 text-sm text-down" aria-live="polite">
			<span>{error}</span>
			<button type="button" on:click={load} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
		</div>
	{/if}

	<div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
		{#each [
			{ label: 'Detected events', value: events.length, icon: 'mdi:shield-alert-outline' },
			{ label: 'Open cases', value: openCases, icon: 'mdi:clipboard-search-outline' },
			{ label: 'Accounts involved', value: involvedAccounts, icon: 'mdi:account-multiple-outline' },
			{ label: 'Observed IPs', value: observedIPs, icon: 'mdi:ip-network-outline' }
		] as metric}
			<div class="rounded-lg border border-stone-800 bg-stone-900/35 p-4">
				<div class="flex items-center justify-between gap-3">
					<span class="text-xs text-stone-500">{metric.label}</span>
					<Icon icon={metric.icon} class="h-4 w-4 text-stone-600" />
				</div>
				<div class="mt-2 text-2xl font-semibold tabular-nums text-stone-100">{loading ? '—' : metric.value}</div>
			</div>
		{/each}
	</div>

	<div>
		<h3 class="mb-3 text-sm font-semibold text-stone-200">Flag-sharing cases</h3>
		{#if loading}
			<div class="flex justify-center rounded-lg border border-stone-800 bg-stone-900/40 p-10">
				<Icon icon="mdi:loading" class="h-5 w-5 animate-spin text-stone-600" />
			</div>
		{:else if events.length === 0}
			<Card hasHeader={false}>
				<EmptyState icon="mdi:shield-check-outline" text="No cases match these filters." />
			</Card>
		{:else}
			<Card hasHeader={false} bodyClass="">
				<div class="divide-y divide-stone-800/60 sm:hidden">
					{#each events as event}
						<button type="button" class="block w-full px-4 py-4 text-left transition-colors hover:bg-stone-800/30 focus:bg-stone-800/30 focus:outline-none" on:click={() => openEvent(event)}>
							<div class="flex items-start justify-between gap-3">
								<div class="min-w-0"><div class="font-medium text-stone-200">{event.challenge_name || event.challenge_id}</div><div class="mt-0.5 truncate font-mono text-[10px] text-stone-600">{event.flag_name} · {event.flag_fingerprint}</div></div>
								<span class="inline-flex shrink-0 rounded-full border px-2 py-1 text-[10px] font-medium {statusClass(event.review_status)}">{statusLabel(event.review_status)}</span>
							</div>
							<div class="mt-3 grid grid-cols-2 gap-4 text-xs">
								<div><div class="text-amber-400/90">{event.owner_username}</div><div class="mt-1 flex items-center gap-1 text-down"><Icon icon="mdi:arrow-right" class="h-3 w-3 shrink-0" />{event.submitter_username}</div></div>
								<div class="text-right"><div class="font-mono text-stone-300">{event.submitter_ip || 'Not retained'}</div><div class="mt-1 tabular-nums text-stone-600" title={instantTitle(event.created_at, 'seconds')}>{formatLocalDateTimeWithZone(event.created_at, 'seconds')}</div></div>
							</div>
						</button>
					{/each}
				</div>
				<div class="hidden overflow-x-auto sm:block">
					<table class="w-full min-w-[820px] table-fixed text-sm">
						<colgroup><col class="w-[22%]" /><col class="w-[22%]" /><col class="w-[18%]" /><col class="w-[14%]" /><col class="w-[24%]" /></colgroup>
						<thead>
							<tr class="border-b border-stone-800 text-xs font-medium text-stone-500">
								<th class="px-4 py-2.5 text-left">Challenge</th>
								<th class="px-4 py-2.5 text-left">Accounts</th>
								<th class="px-4 py-2.5 text-left">Observed IP</th>
								<th class="px-4 py-2.5 text-left">Review</th>
								<th class="px-4 py-2.5 text-right">Time</th>
							</tr>
						</thead>
						<tbody>
							{#each events as event}
								<tr
									class="cursor-pointer border-b border-stone-800/60 transition-colors hover:bg-stone-800/30 focus:bg-stone-800/30 focus:outline-none"
									role="button"
									tabindex="0"
									on:click={() => openEvent(event)}
									on:keydown={(keyEvent) => handleRowKey(keyEvent, event)}
								>
									<td class="align-middle px-4 py-3">
										<div class="font-medium text-stone-200">{event.challenge_name || event.challenge_id}</div>
										<div class="mt-0.5 font-mono text-[10px] text-stone-600">{event.flag_name} · {event.flag_fingerprint}</div>
									</td>
									<td class="align-middle px-4 py-3">
										<div class="text-xs text-amber-400/90">{event.owner_username}</div>
										<div class="mt-0.5 flex items-center gap-1 text-xs text-down"><Icon icon="mdi:arrow-right" class="h-3 w-3" />{event.submitter_username}</div>
									</td>
									<td class="align-middle px-4 py-3">
										<div class="font-mono text-xs text-stone-300">{event.submitter_ip || 'Not retained'}</div>
										{#if locationLabel(event)}<div class="mt-0.5 text-[10px] text-stone-600">{locationLabel(event)}</div>{/if}
									</td>
									<td class="align-middle px-4 py-3"><span class="inline-flex rounded-full border px-2 py-1 text-[10px] font-medium {statusClass(event.review_status)}">{statusLabel(event.review_status)}</span></td>
									<td class="whitespace-nowrap px-4 py-3 text-right align-middle text-xs tabular-nums text-stone-500" title={instantTitle(event.created_at, 'seconds')}>{formatLocalDateTimeWithZone(event.created_at, 'seconds')}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</Card>
		{/if}
	</div>

	<div class="rounded-lg border border-stone-800 bg-stone-900/25">
		<button type="button" on:click={() => showInstanceFlags = !showInstanceFlags} class="flex w-full items-center justify-between gap-4 px-4 py-3 text-left">
			<div>
				<div class="text-sm font-semibold text-stone-300">Active instance flags</div>
				<div class="mt-0.5 text-xs text-stone-600">{instanceFlags.length} active</div>
			</div>
			<Icon icon={showInstanceFlags ? 'mdi:chevron-up' : 'mdi:chevron-down'} class="h-4 w-4 text-stone-600" />
		</button>
		{#if showInstanceFlags}
			<div class="overflow-x-auto border-t border-stone-800">
				<table class="w-full min-w-[680px] text-sm">
					<thead><tr class="border-b border-stone-800 text-xs font-medium text-stone-500"><th class="px-4 py-2.5 text-left">User</th><th class="px-4 py-2.5 text-left">Challenge</th><th class="px-4 py-2.5 text-left">Fingerprint</th><th class="px-4 py-2.5 text-left">Instance</th></tr></thead>
					<tbody>
						{#each instanceFlags as flag}
							<tr class="border-b border-stone-800/60"><td class="px-4 py-2.5 text-stone-200">{flag.username || flag.user_id}</td><td class="px-4 py-2.5 text-stone-300">{flag.challenge_name || flag.challenge_id}</td><td class="max-w-xs truncate px-4 py-2.5 font-mono text-xs text-stone-400">sha256:{flag.flag_fingerprint} · {flag.flag_length} chars</td><td class="px-4 py-2.5 font-mono text-xs text-stone-500">{flag.instance_id}</td></tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
</div>

{#if selected}
	<div class="fixed inset-0 z-[70] flex items-center justify-center p-4">
		<button type="button" aria-label="Close evidence detail" class="fixed inset-0 bg-stone-950/85 backdrop-blur-sm" on:click={closeEvent}></button>
		<div class="relative z-10 flex max-h-[92vh] w-full max-w-4xl flex-col overflow-hidden rounded-xl border border-stone-800 bg-stone-950 shadow-2xl" role="dialog" aria-modal="true" aria-labelledby="evidence-title">
			<div class="flex items-start justify-between gap-4 border-b border-stone-800 px-5 py-4 sm:px-6">
				<div>
					<div class="flex flex-wrap items-center gap-2">
						<h2 id="evidence-title" class="text-lg font-semibold text-stone-100">{selected.challenge_name}</h2>
						<span class="inline-flex rounded-full border px-2 py-1 text-[10px] font-medium {statusClass(selected.review_status)}">{statusLabel(selected.review_status)}</span>
					</div>
					<p class="mt-1 font-mono text-xs text-stone-600">Case {selected.id}</p>
				</div>
				<button type="button" on:click={closeEvent} class="rounded-md p-1.5 text-stone-500 hover:bg-stone-900 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button>
			</div>

			<div class="overflow-y-auto px-5 py-5 sm:px-6">
				<div class="grid gap-4 md:grid-cols-2">
					<div class="rounded-lg border border-amber-500/20 bg-amber-500/5 p-4">
						<div class="mb-3 flex items-center gap-2 font-sans text-sm font-semibold text-amber-200"><Icon icon="mdi:flag-outline" class="h-4 w-4" />Original recipient</div>
						<dl class="space-y-3 text-sm">
							<div><dt class="text-xs text-stone-600">Account</dt><dd class="mt-0.5 text-stone-200">{selected.owner_username}</dd><dd class="break-all text-xs text-stone-500">{selected.owner_email || 'No email'}</dd></div>
							<div><dt class="text-xs text-stone-600">Team</dt><dd class="mt-0.5 text-stone-300">{selected.owner_team_name || 'No team'}</dd></div>
							<div><dt class="text-xs text-stone-600">Matching submission IP</dt><dd class="mt-0.5 font-mono text-xs text-stone-300">{selected.owner_ip || 'Not available'}</dd></div>
							<div><dt class="text-xs text-stone-600">Client</dt><dd class="mt-0.5 text-stone-300">{deviceLabel(selected.owner_user_agent)}</dd></div>
						</dl>
					</div>
					<div class="rounded-lg border border-down/20 bg-down/5 p-4">
						<div class="mb-3 flex items-center gap-2 font-sans text-sm font-semibold text-down"><Icon icon="mdi:account-arrow-right-outline" class="h-4 w-4" />Submitting account</div>
						<dl class="space-y-3 text-sm">
							<div><dt class="text-xs text-stone-600">Account</dt><dd class="mt-0.5 text-stone-200">{selected.submitter_username}</dd><dd class="break-all text-xs text-stone-500">{selected.submitter_email || 'No email'}</dd></div>
							<div><dt class="text-xs text-stone-600">Team</dt><dd class="mt-0.5 text-stone-300">{selected.submitter_team_name || 'No team'}</dd></div>
							<div><dt class="text-xs text-stone-600">Observed IP</dt><dd class="mt-0.5 font-mono text-xs text-stone-300">{selected.submitter_ip || 'Not retained'}</dd>{#if locationLabel(selected)}<dd class="text-[10px] text-stone-600">{locationLabel(selected)}</dd>{/if}</div>
							<div><dt class="text-xs text-stone-600">Client</dt><dd class="mt-0.5 text-stone-300">{deviceLabel(selected.submitter_user_agent)}</dd></div>
						</dl>
					</div>
				</div>

				<div class="mt-4 grid gap-3 rounded-lg border border-stone-800 bg-stone-900/30 p-4 text-xs sm:grid-cols-2 lg:grid-cols-4">
					<div><div class="text-stone-600">Flag fingerprint</div><div class="mt-1 break-all font-mono text-stone-300">{selected.flag_fingerprint}</div></div>
					<div><div class="text-stone-600">Flag</div><div class="mt-1 text-stone-300">{selected.flag_name}</div></div>
					<div><div class="text-stone-600">Request ID</div><div class="mt-1 break-all font-mono text-stone-300">{selected.request_id || 'Not retained'}</div></div>
					<div><div class="text-stone-600">Detected</div><div class="mt-1 text-stone-300" title={instantTitle(selected.created_at, 'seconds')}>{formatLocalDateTimeWithZone(selected.created_at, 'seconds')}</div></div>
				</div>

				<div class="mt-5 rounded-lg border border-stone-800 p-4">
					<div class="mb-3 flex flex-wrap items-center justify-between gap-3">
						<div class="text-sm font-semibold text-stone-200">Review decision</div>
						{#if selected.submitter_user_id}
							<div class="flex flex-wrap items-center gap-2">
								<button type="button" on:click={warnSubmitter} disabled={warningSaving} class="inline-flex h-8 items-center gap-1.5 rounded-md border border-amber-500/30 px-3 text-xs font-medium text-amber-300 transition-colors hover:bg-amber-500/10 disabled:opacity-50">
									{#if warningSaving}<Icon icon="mdi:loading" class="h-3.5 w-3.5 animate-spin" />{:else}<Icon icon="mdi:message-alert-outline" class="h-3.5 w-3.5" />{/if}
									Warn participant
								</button>
								<button type="button" on:click={toggleSubmitterBan} disabled={accountSaving} class="inline-flex h-8 items-center gap-1.5 rounded-md border px-3 text-xs font-medium transition-colors disabled:opacity-50 {selected.submitter_status === 'banned' ? 'border-up/30 text-up hover:bg-up/10' : 'border-down/30 text-down hover:bg-down/10'}">
									{#if accountSaving}<Icon icon="mdi:loading" class="h-3.5 w-3.5 animate-spin" />{:else}<Icon icon={selected.submitter_status === 'banned' ? 'mdi:account-check-outline' : 'mdi:account-cancel-outline'} class="h-3.5 w-3.5" />{/if}
									{selected.submitter_status === 'banned' ? 'Unban account' : 'Ban submitting account'}
								</button>
							</div>
						{/if}
					</div>
					<div class="grid gap-3 sm:grid-cols-[190px_minmax(0,1fr)]">
						<label><span class="mb-1.5 block text-xs text-stone-500">Status</span><select bind:value={reviewStatus} class="h-10 w-full rounded-md border border-stone-800 bg-stone-950 px-3 text-sm text-stone-200 outline-none focus:border-stone-600"><option value="open">Open</option><option value="reviewing">In review</option><option value="confirmed">Confirmed</option><option value="dismissed">Dismissed</option></select></label>
						<label><span class="mb-1.5 block text-xs text-stone-500">Reviewer note</span><textarea bind:value={reviewNote} maxlength="2000" rows="3" placeholder="Record the evidence considered and any follow-up." class="w-full resize-y rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-200 outline-none placeholder:text-stone-700 focus:border-stone-600"></textarea></label>
					</div>
					{#if reviewError}<p class="mt-2 text-xs text-down" aria-live="polite">{reviewError}</p>{/if}
					<div class="mt-3 flex justify-end"><button type="button" on:click={saveReview} disabled={reviewSaving} class="inline-flex h-9 items-center gap-2 rounded-md bg-stone-100 px-4 text-xs font-semibold text-stone-950 transition-colors hover:bg-white disabled:opacity-50">{#if reviewSaving}<Icon icon="mdi:loading" class="h-3.5 w-3.5 animate-spin" />{/if}Save review</button></div>
				</div>
			</div>
		</div>
	</div>
{/if}
