<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import Icon from '@iconify/svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { formatLocalDateTimeWithZone, instantTitle } from '$lib/time';

	export let seed: any;
	export let detail: any = null;
	export let loading = false;
	export let error = '';
	export let adjusting = false;
	export let action = '';
	export let economyEnabled = false;

	const dispatch = createEventDispatcher();
	let tab = 'overview';
	let adjustmentAmount = 0;
	let adjustmentKind = 'admin_bonus';
	let adjustmentNote = '';
	let memberUsername = '';

	$: team = detail?.team ?? seed ?? {};
	$: tabs = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'members', label: 'Members', count: detail?.members?.length ?? team.member_count ?? 0 },
		{ id: 'submissions', label: 'Submissions', count: team.submission_count ?? detail?.submissions?.length ?? 0 },
		{ id: 'ips', label: 'IP activity', count: detail?.access_ips?.length ?? 0 },
		{ id: 'instances', label: 'Instances', count: detail?.instances?.length ?? 0 },
		...(economyEnabled ? [{ id: 'ledger', label: 'Ledger', count: detail?.credit_events?.length ?? 0 }] : []),
		{ id: 'audit', label: 'Admin history', count: detail?.audit?.length ?? 0 }
	];

	function when(value: number | undefined) {
		return value ? formatLocalDateTimeWithZone(value, 'seconds') : '—';
	}

	function stateClass(value: string) {
		if (['active', 'running', 'solved'].includes(value)) return 'text-up';
		if (['banned', 'failed', 'wrong'].includes(value)) return 'text-down';
		if (['open', 'creating', 'pending', 'stopping'].includes(value)) return 'text-warn';
		return 'text-stone-500';
	}

	function submitAdjustment() {
		if (!Number.isFinite(Number(adjustmentAmount)) || Number(adjustmentAmount) === 0 || !adjustmentNote.trim()) return;
		dispatch('credit', { amount: Number(adjustmentAmount), kind: adjustmentKind, note: adjustmentNote.trim() });
	}

	function addMember() {
		const username = memberUsername.trim();
		if (!username || action) return;
		dispatch('addmember', { username });
		memberUsername = '';
	}

	function canStop(status: string) {
		return ['pending', 'creating', 'starting', 'running', 'active', 'ready', 'stopping'].includes(status);
	}
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center p-2 sm:p-4">
	<button type="button" aria-label="Close team details" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => dispatch('close')}></button>
	<div class="relative z-10 flex max-h-[96vh] w-full max-w-7xl flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-950 shadow-2xl shadow-black/30" role="dialog" aria-modal="true" aria-label="Team details">
		<header class="flex items-start justify-between gap-4 border-b border-stone-800 px-4 py-4 sm:px-6">
			<div class="min-w-0">
				<p class="metadata-label text-stone-500">Team dossier</p>
				<h2 class="mt-1 truncate text-xl font-semibold text-stone-100">{team.name}</h2>
				<p class="mt-1 text-xs text-stone-600">Created {when(team.created_at)}{team.created_by ? ` by ${team.created_by}` : ''}</p>
			</div>
			<div class="flex shrink-0 items-center gap-3">
				<a href="/team/{team.id}" class="hidden text-xs text-stone-400 hover:text-stone-200 sm:inline">Public profile</a>
				<button type="button" on:click={() => dispatch('close')} class="rounded p-1 text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button>
			</div>
		</header>

		<nav class="flex shrink-0 gap-1 overflow-x-auto border-b border-stone-800 px-3 sm:px-6" aria-label="Team dossier sections">
			{#each tabs as item}
				<button type="button" on:click={() => (tab = item.id)} class="shrink-0 border-b-2 px-3 py-3 text-xs font-medium transition-colors {tab === item.id ? 'border-amber-500 text-stone-100' : 'border-transparent text-stone-500 hover:text-stone-300'}">
					{item.label}{#if item.count !== undefined}<span class="ml-1.5 tabular-nums text-stone-600">{item.count}</span>{/if}
				</button>
			{/each}
		</nav>

		<div class="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
			{#if loading}
				<div class="grid min-h-72 place-items-center"><Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-600" /></div>
			{:else if error}
				<div class="rounded-md border border-down/30 bg-down/10 p-4 text-sm text-down">{error}</div>
			{:else if detail}
				{#if tab === 'overview'}
					<div class="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
						{#each [
							...(economyEnabled ? [
								{ label: 'Ledger points', value: Math.round(team.ledger_points ?? team.total_score ?? 0).toLocaleString(), accent: true },
								{ label: 'Credits', value: Number(team.ledger_credits ?? 0).toLocaleString(undefined, { maximumFractionDigits: 3 }) }
							] : [{ label: 'Score', value: Math.round(team.total_score ?? team.legacy_score ?? 0).toLocaleString(), accent: true }]),
							{ label: 'Challenges', value: team.challenge_solves ?? 0 },
							{ label: 'Flags', value: team.flag_solves ?? 0 },
							{ label: 'Members', value: team.member_count ?? 0 },
							{ label: 'Attempts', value: team.submission_count ?? 0 },
							{ label: 'IP addresses', value: detail.access_ips?.length ?? 0 },
							{ label: 'Legacy score', value: team.legacy_score ?? 0 }
						] as item}
							<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-3">
								<p class="metadata-label text-stone-600">{item.label}</p>
								<p class="mt-1 text-xl font-semibold tabular-nums {item.accent ? 'text-amber-500' : 'text-stone-100'}">{item.value}</p>
							</div>
						{/each}
					</div>

					<div class="mt-5 grid items-start gap-4 lg:grid-cols-2">
						<Card title="Competition state" bodyClass="p-4">
							<div class="grid grid-cols-2 gap-4 text-xs sm:grid-cols-3">
								<div><p class="metadata-label text-stone-600">Open slots</p><p class="mt-1 text-stone-200">{detail.support?.open_count ?? 0}/{detail.support?.concurrency_cap ?? 0}</p></div>
								<div><p class="metadata-label text-stone-600">Correct</p><p class="mt-1 text-up">{seed.correct_submissions ?? detail.submissions?.filter((row: any) => row.correct).length ?? 0}</p></div>
								<div><p class="metadata-label text-stone-600">Wrong</p><p class="mt-1 text-down">{seed.wrong_submissions ?? detail.submissions?.filter((row: any) => !row.correct).length ?? 0}</p></div>
								<div><p class="metadata-label text-stone-600">Bailout</p><p class="mt-1 text-stone-300">{team.bailout_used ? 'Used' : 'Available'}</p></div>
								<div><p class="metadata-label text-stone-600">Grant</p><p class="mt-1 text-stone-300">{team.grant_issued ? 'Issued' : 'Not issued'}</p></div>
								<div><p class="metadata-label text-stone-600">Max members</p><p class="mt-1 text-stone-300">{team.max_members ?? 'Unlimited'}</p></div>
							</div>
						</Card>
						<Card title="Open challenges" bodyClass="p-0">
							{#if detail.support?.opens?.filter((row: any) => row.status === 'open').length}
								<div class="divide-y divide-stone-800/60">
									{#each detail.support.opens.filter((row: any) => row.status === 'open') as row}
										<a href="/challenges/{row.slug}" class="flex items-center justify-between gap-3 px-4 py-3 text-xs hover:bg-stone-900/50"><span class="text-stone-300">{row.name}</span><span class="tabular-nums text-stone-600">{when(row.expires_at)}</span></a>
									{/each}
								</div>
							{:else}<EmptyState icon="mdi:lock-open-outline" text="No challenge slots are open." />{/if}
						</Card>
					</div>

					<Card title={`Recent solves (${detail.solves?.length ?? 0})`} bodyClass="p-0" className="mt-4">
						{#if detail.solves?.length}
							<div class="max-h-80 divide-y divide-stone-800/60 overflow-y-auto">
								{#each detail.solves as solve}
									<div class="flex items-center justify-between gap-3 px-4 py-3 text-xs"><div class="min-w-0"><p class="truncate text-stone-300">{solve.challenge_name}{solve.flag_name ? ` · ${solve.flag_name}` : ''}</p><p class="mt-1 text-stone-600">{solve.solver_username} · {when(solve.solved_at)}</p></div><span class="shrink-0 tabular-nums text-up">+{solve.points}</span></div>
								{/each}
							</div>
						{:else}<EmptyState icon="mdi:flag-outline" text="No solves recorded." />{/if}
					</Card>

					{#if economyEnabled}<Card title="Ledger adjustment" bodyClass="p-4" className="mt-4">
						<form class="grid items-end gap-3 sm:grid-cols-[10rem_10rem_1fr_auto]" on:submit|preventDefault={submitAdjustment}>
							<label><span class="metadata-label mb-1.5 block text-stone-600">Reason</span><select bind:value={adjustmentKind} class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-300 focus:border-stone-600 focus:outline-none"><option value="admin_bonus">Bonus</option><option value="admin_refund">Refund</option><option value="admin_penalty">Penalty</option><option value="admin_correction">Correction</option></select></label>
							<label><span class="metadata-label mb-1.5 block text-stone-600">Credits</span><input type="number" bind:value={adjustmentAmount} step="0.001" min="-20000" max="20000" required class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm tabular-nums text-stone-300 focus:border-stone-600 focus:outline-none" /></label>
							<label><span class="metadata-label mb-1.5 block text-stone-600">Audit note</span><input type="text" bind:value={adjustmentNote} maxlength="240" required placeholder="Why this adjustment is required" class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-300 placeholder-stone-700 focus:border-stone-600 focus:outline-none" /></label>
							<button type="submit" disabled={adjusting || !adjustmentNote.trim() || Number(adjustmentAmount) === 0} class="inline-flex h-10 items-center justify-center rounded-md bg-amber-500 px-4 text-sm font-medium text-stone-950 transition-colors hover:bg-amber-400 disabled:cursor-not-allowed disabled:opacity-40">{adjusting ? 'Applying…' : 'Apply'}</button>
						</form>
						<p class="mt-2 text-xs text-stone-600">Positive values grant credits; negative values deduct them. Every change is atomic and appears in the team Ledger.</p>
					</Card>{/if}
				{:else if tab === 'members'}
					<form class="mb-4 flex flex-col gap-2 rounded-lg border border-stone-800 bg-stone-900/30 p-3 sm:flex-row sm:items-end" on:submit|preventDefault={addMember}>
						<label class="min-w-0 flex-1"><span class="metadata-label mb-1.5 block text-stone-600">Add or move participant by username</span><input type="text" bind:value={memberUsername} autocomplete="off" placeholder="username" class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-300 placeholder-stone-700 focus:border-stone-600 focus:outline-none" /></label>
						<button type="submit" disabled={!memberUsername.trim() || !!action} class="inline-flex h-10 items-center justify-center gap-2 rounded-md border border-stone-700 px-4 text-sm font-medium text-stone-300 hover:border-stone-600 hover:text-stone-100 disabled:cursor-not-allowed disabled:opacity-40">{#if action === 'add-member'}<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />{/if}Add member</button>
					</form>
					<div class="grid gap-3 lg:grid-cols-2">
						{#each detail.members ?? [] as member}
							<div class="rounded-lg border border-stone-800 bg-stone-900/30 p-4 transition-colors hover:border-stone-700 hover:bg-stone-900/60">
								<button type="button" on:click={() => dispatch('user', member)} class="block w-full text-left">
									<div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="truncate text-sm font-medium text-stone-200">{member.display_name || member.username}</p><p class="mt-1 truncate text-xs text-stone-600">@{member.username} · {member.email || 'No email'}</p></div><span class="text-xs {stateClass(member.status)}">{member.status}</span></div>
									<div class="mt-4 grid grid-cols-3 gap-3 text-xs"><div><p class="metadata-label text-stone-600">Challenges</p><p class="mt-1 text-stone-300">{member.challenge_solves}</p></div><div><p class="metadata-label text-stone-600">Attempts</p><p class="mt-1 text-stone-300">{member.submission_count}</p></div><div><p class="metadata-label text-stone-600">Last IP</p><p class="mt-1 truncate font-mono text-stone-400">{member.last_login_ip || '—'}</p></div></div>
								</button>
								<div class="mt-3 flex items-center justify-between border-t border-stone-800/70 pt-3"><button type="button" on:click={() => dispatch('user', member)} class="text-xs text-stone-500 hover:text-stone-200">Open participant dossier</button><button type="button" disabled={!!action} on:click={() => dispatch('removemember', member)} class="inline-flex items-center gap-1.5 text-xs text-down/80 hover:text-down disabled:cursor-not-allowed disabled:opacity-40">{#if action === `remove-${member.id}`}<Icon icon="mdi:loading" class="h-3.5 w-3.5 animate-spin" />{/if}Remove from team</button></div>
							</div>
						{/each}
					</div>
				{:else if tab === 'submissions'}
					<div class="mb-3 rounded-md border border-stone-800 bg-stone-900/30 px-3 py-2 text-xs text-stone-500">Raw flag material is never returned. The fingerprint, length, result, participant, IP, user agent, instance and timestamp preserve the useful audit trail.</div>
					{#if detail.submissions?.length}
						<div class="overflow-x-auto rounded-lg border border-stone-800">
							<table class="w-full min-w-[960px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Participant</th><th class="px-3 py-2 text-left">Challenge</th><th class="px-3 py-2 text-left">Result</th><th class="px-3 py-2 text-left">Fingerprint</th><th class="px-3 py-2 text-left">IP</th><th class="px-3 py-2 text-left">Client</th><th class="px-3 py-2 text-right">When</th></tr></thead><tbody>
								{#each detail.submissions as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.username}</td><td class="px-3 py-2 text-stone-300">{row.challenge_name}{row.flag_name ? ` · ${row.flag_name}` : ''}</td><td class="px-3 py-2 {row.correct ? 'text-up' : 'text-down'}">{row.correct ? 'Correct' : 'Wrong'}</td><td class="px-3 py-2 font-mono text-stone-500">{row.flag_fingerprint} · {row.flag_length}</td><td class="px-3 py-2 font-mono text-stone-400">{row.ip_address || '—'}</td><td class="max-w-64 truncate px-3 py-2 text-stone-600" title={row.user_agent}>{row.user_agent || '—'}</td><td class="px-3 py-2 text-right text-stone-600" title={instantTitle(row.submitted_at, 'seconds')}>{when(row.submitted_at)}</td></tr>{/each}
							</tbody></table>
						</div>
					{:else}<EmptyState icon="mdi:form-textbox-password" text="No submissions recorded." />{/if}
				{:else if tab === 'ips'}
					{#if detail.access_ips?.length}
						<div class="overflow-x-auto rounded-lg border border-stone-800"><table class="w-full min-w-[700px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Participant</th><th class="px-3 py-2 text-left">IP address</th><th class="px-3 py-2 text-left">Sources</th><th class="px-3 py-2 text-right">Events</th><th class="px-3 py-2 text-right">First seen</th><th class="px-3 py-2 text-right">Last seen</th></tr></thead><tbody>{#each detail.access_ips as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.username}</td><td class="px-3 py-2 font-mono text-stone-300">{row.ip_address}</td><td class="px-3 py-2 text-stone-500">{row.sources?.join(', ')}</td><td class="px-3 py-2 text-right text-stone-400">{row.events}</td><td class="px-3 py-2 text-right text-stone-600">{when(row.first_seen_at)}</td><td class="px-3 py-2 text-right text-stone-500">{when(row.last_seen_at)}</td></tr>{/each}</tbody></table></div>
					{:else}<EmptyState icon="mdi:ip-network-outline" text="No IP activity recorded." />{/if}
				{:else if tab === 'instances'}
					{#if detail.instances?.length}
						<div class="space-y-2">{#each detail.instances as row}<div class="rounded-lg border border-stone-800 bg-stone-900/30 p-3 text-xs"><div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="text-stone-300">{row.challenge_name} · {row.username || 'Unknown owner'}</p><p class="mt-1 break-all font-mono text-stone-600">{row.id}{row.target ? ` · ${row.target}` : ''}</p></div><div class="flex shrink-0 items-center gap-3"><span class="{stateClass(row.status)}">{row.status}</span>{#if canStop(row.status)}<button type="button" disabled={!!action} on:click={() => dispatch('stopinstance', row)} class="inline-flex items-center gap-1.5 rounded border border-down/30 px-2 py-1 text-down/80 hover:border-down/60 hover:text-down disabled:cursor-not-allowed disabled:opacity-40">{#if action === `stop-${row.id}`}<Icon icon="mdi:loading" class="h-3.5 w-3.5 animate-spin" />{/if}Force stop</button>{/if}</div></div><div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-stone-600"><span>Created {when(row.created_at)}</span>{#if row.started_at}<span>Started {when(row.started_at)}</span>{/if}{#if row.expires_at}<span>Expires {when(row.expires_at)}</span>{/if}{#if row.stopped_at}<span>Stopped {when(row.stopped_at)}</span>{/if}</div>{#if row.error_message}<p class="mt-2 text-down">{row.error_message}</p>{/if}</div>{/each}</div>
					{:else}<EmptyState icon="mdi:cube-off-outline" text="No instance history." />{/if}
				{:else if tab === 'ledger'}
					{#if detail.credit_events?.length}
						<div class="overflow-x-auto rounded-lg border border-stone-800"><table class="w-full min-w-[680px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Event</th><th class="px-3 py-2 text-left">Challenge / instance</th><th class="px-3 py-2 text-right">Amount</th><th class="px-3 py-2 text-right">Balance</th><th class="px-3 py-2 text-right">When</th></tr></thead><tbody>{#each detail.credit_events as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.kind}</td><td class="px-3 py-2 font-mono text-stone-600">{row.challenge_id || row.instance_id || '—'}</td><td class="px-3 py-2 text-right tabular-nums {row.amount >= 0 ? 'text-up' : 'text-down'}">{row.amount >= 0 ? '+' : ''}{Number(row.amount).toFixed(3)}</td><td class="px-3 py-2 text-right tabular-nums text-stone-400">{Number(row.balance_after).toFixed(3)}</td><td class="px-3 py-2 text-right text-stone-600">{when(row.created_at)}</td></tr>{/each}</tbody></table></div>
					{:else}<EmptyState icon="mdi:book-open-variant" text="No Ledger activity." />{/if}
				{:else if tab === 'audit'}
					{#if detail.audit?.length}<div class="space-y-2">{#each detail.audit as row}<div class="rounded-lg border border-stone-800 bg-stone-900/30 p-3 text-xs"><div class="flex flex-wrap items-center justify-between gap-2"><p class="font-medium text-stone-300">{row.action.replaceAll('_', ' ')}</p><span class="text-stone-600">{when(row.created_at)}</span></div><p class="mt-1 text-stone-600">{row.actor || 'System'} · {row.ip_address || 'No IP'}</p>{#if row.new_values}<pre class="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded bg-stone-950 p-2 font-mono text-[10px] text-stone-500">{JSON.stringify(row.new_values, null, 2)}</pre>{/if}</div>{/each}</div>{:else}<EmptyState icon="mdi:clipboard-text-clock-outline" text="No administrative changes recorded." />{/if}
				{/if}
			{/if}
		</div>
	</div>
</div>
