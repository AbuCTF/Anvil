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
	export let economyEnabled = false;

	const dispatch = createEventDispatcher();
	let tab = 'overview';

	$: user = detail?.user ?? seed ?? {};
	$: administrativeHistory = (detail?.audit ?? []).filter((event: any) => event.action !== 'user.login');
	$: tabs = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'competition', label: 'Competition', count: (detail?.solves?.length ?? 0) + (detail?.instances?.length ?? 0) },
		{ id: 'access', label: 'Access', count: detail?.access_summary?.login_count ?? 0 },
		{ id: 'moderation', label: 'Moderation', count: (detail?.warnings?.length ?? 0) + administrativeHistory.length }
	];
	$: recentActivity = detail ? [
		...(detail.login_history ?? []).map((row: any) => ({ kind: 'Sign in', label: row.ip_address || 'IP not retained', at: row.logged_in_at, tone: 'text-stone-300' })),
		...(detail.solves ?? []).map((row: any) => ({ kind: 'Solve', label: row.challenge_name, at: row.solved_at, tone: 'text-up' })),
		...(detail.submissions ?? []).map((row: any) => ({ kind: row.correct ? 'Correct submission' : 'Wrong submission', label: row.challenge_name, at: row.submitted_at, tone: row.correct ? 'text-up' : 'text-down' })),
		...(detail.instances ?? []).map((row: any) => ({ kind: 'Instance', label: `${row.challenge_name} · ${row.status}`, at: row.created_at, tone: row.status === 'failed' ? 'text-down' : 'text-stone-300' }))
	].sort((a: any, b: any) => b.at - a.at).slice(0, 10) : [];

	function when(value: number | undefined) {
		return value ? formatLocalDateTimeWithZone(value, 'seconds') : 'Not recorded';
	}

	function stateClass(value: string) {
		if (['active', 'running', 'correct'].includes(value)) return 'text-up';
		if (['banned', 'failed', 'wrong'].includes(value)) return 'text-down';
		return 'text-stone-500';
	}
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center p-2 sm:p-4">
	<button type="button" aria-label="Close participant details" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => dispatch('close')}></button>
	<div class="relative z-10 flex max-h-[96vh] w-full max-w-7xl flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-950 shadow-2xl shadow-black/30" role="dialog" aria-modal="true" aria-label="Participant details">
		<header class="flex items-start justify-between gap-4 border-b border-stone-800 px-4 py-4 sm:px-6">
			<div class="min-w-0">
				<div class="flex flex-wrap items-center gap-2">
					<h2 class="truncate text-xl font-semibold text-stone-100">{user.display_name || user.username}</h2>
					<span class="rounded-full border border-stone-800 px-2 py-0.5 text-[10px] uppercase tracking-wider {stateClass(user.status)}">{user.status || 'active'}</span>
					{#if user.role === 'admin'}<span class="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] uppercase tracking-wider text-amber-500">Admin</span>{/if}
				</div>
				<p class="mt-1 truncate text-xs text-stone-600">@{user.username}{user.email ? ` · ${user.email}` : ''}</p>
			</div>
			<div class="flex shrink-0 items-center gap-3">
				<a href="/profile/{encodeURIComponent(user.username)}" class="hidden text-xs text-stone-400 hover:text-stone-200 sm:inline">Public profile</a>
				<button type="button" on:click={() => dispatch('warn')} class="text-xs text-warn hover:text-amber-300">Warn</button>
				<button type="button" on:click={() => dispatch('toggleban')} class="text-xs {user.status === 'banned' ? 'text-up' : 'text-down'} hover:opacity-80">{user.status === 'banned' ? 'Unban' : 'Ban'}</button>
				<button type="button" on:click={() => dispatch('close')} class="rounded p-1 text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button>
			</div>
		</header>

		<nav class="flex shrink-0 gap-1 overflow-x-auto border-b border-stone-800 px-3 sm:px-6" aria-label="Participant dossier sections">
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
					<div class="grid grid-cols-2 gap-3 md:grid-cols-4">
						{#each [
							{ label: economyEnabled ? 'Ledger score' : 'Score', value: Math.round(user.total_score ?? 0).toLocaleString() },
							{ label: 'Solves', value: user.solve_count ?? 0 },
							{ label: 'Correct', value: user.correct_submissions ?? 0 },
							{ label: 'Wrong', value: user.wrong_submissions ?? 0 }
						] as item}
							<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-3"><p class="metadata-label text-stone-600">{item.label}</p><p class="mt-1 text-xl font-semibold tabular-nums text-stone-100">{item.value}</p></div>
						{/each}
					</div>

					<div class="mt-5 grid items-start gap-4 lg:grid-cols-2">
						<Card title="Account" bodyClass="p-4">
							<div class="grid grid-cols-2 gap-x-4 gap-y-4 text-xs">
								<div><p class="metadata-label text-stone-600">Role</p><p class="mt-1 capitalize text-stone-300">{user.role}</p></div>
								<div><p class="metadata-label text-stone-600">Email</p><p class="mt-1 text-stone-300">{user.email_verified ? 'Verified' : 'Unverified'}</p></div>
								<div><p class="metadata-label text-stone-600">Joined</p><p class="mt-1 text-stone-300">{when(user.created_at)}</p></div>
								<div><p class="metadata-label text-stone-600">Last sign in</p><p class="mt-1 text-stone-300">{when(detail.access_summary?.last_login_at)}</p></div>
								<div class="col-span-2"><p class="metadata-label text-stone-600">Team</p><p class="mt-1 text-stone-300">{detail.team?.name ?? 'No team'}{detail.team && economyEnabled ? ` · ${Math.floor(detail.team.credits ?? 0)} credits · ${Math.round(detail.team.points ?? 0)} points` : ''}</p></div>
								{#if user.bio}<div class="col-span-2"><p class="metadata-label text-stone-600">Bio</p><p class="mt-1 whitespace-pre-wrap text-stone-400">{user.bio}</p></div>{/if}
							</div>
						</Card>
						<Card title="Recent activity" bodyClass="p-0">
							{#if recentActivity.length}
								<div class="max-h-72 divide-y divide-stone-800/60 overflow-y-auto">
									{#each recentActivity as event}
										<div class="flex items-start justify-between gap-4 px-4 py-3 text-xs"><div class="min-w-0"><p class={event.tone}>{event.kind}</p><p class="mt-1 truncate text-stone-500">{event.label}</p></div><span class="shrink-0 text-stone-600" title={instantTitle(event.at, 'seconds')}>{when(event.at)}</span></div>
									{/each}
								</div>
							{:else}<EmptyState icon="mdi:history" text="No recorded activity." />{/if}
						</Card>
					</div>

				{:else if tab === 'competition'}
					<div class="grid items-start gap-4 lg:grid-cols-2">
						<Card title={`Solve history (${detail.solves?.length ?? 0})`} bodyClass="p-0">
							{#if detail.solves?.length}<div class="max-h-80 divide-y divide-stone-800/60 overflow-y-auto">{#each detail.solves as solve}<div class="flex items-center justify-between gap-3 px-4 py-3 text-xs"><div><p class="text-stone-300">{solve.challenge_name}{solve.flag_name ? ` · ${solve.flag_name}` : ''}</p><p class="mt-1 text-stone-600">{when(solve.solved_at)}</p></div><span class="shrink-0 tabular-nums text-up">+{solve.points}</span></div>{/each}</div>{:else}<EmptyState icon="mdi:flag-outline" text="No solves." />{/if}
						</Card>
						<Card title={`Instance history (${detail.instances?.length ?? 0})`} bodyClass="p-0">
							{#if detail.instances?.length}<div class="max-h-80 divide-y divide-stone-800/60 overflow-y-auto">{#each detail.instances as instance}<div class="flex items-start justify-between gap-3 px-4 py-3 text-xs"><div class="min-w-0"><p class="text-stone-300">{instance.challenge_name}</p><p class="mt-1 break-all font-mono text-stone-600">{instance.id}</p><p class="mt-1 text-stone-600">{when(instance.created_at)}</p>{#if instance.error_message}<p class="mt-1 text-down">{instance.error_message}</p>{/if}</div><span class="shrink-0 {stateClass(instance.status)}">{instance.status}</span></div>{/each}</div>{:else}<EmptyState icon="mdi:cube-off-outline" text="No instances." />{/if}
						</Card>
					</div>
					<div class="mt-4"><Card title={`Submission trail (${user.submission_count ?? 0})`} bodyClass="p-0">
						{#if detail.submissions?.length}<div class="max-h-[28rem] divide-y divide-stone-800/60 overflow-y-auto">{#each detail.submissions as submission}<div class="flex items-start justify-between gap-3 px-4 py-3 text-xs"><div class="min-w-0"><p class="text-stone-300">{submission.challenge_name}{submission.flag_name ? ` · ${submission.flag_name}` : ''}</p><p class="mt-1 font-mono text-stone-600">sha256:{submission.flag_fingerprint} · {submission.flag_length} chars · {submission.ip_address ?? 'IP not retained'}</p>{#if submission.instance_id}<p class="mt-1 break-all font-mono text-stone-700">instance {submission.instance_id}</p>{/if}<p class="mt-1 truncate text-stone-600" title={submission.user_agent}>{submission.user_agent || 'Client not recorded'}</p><p class="mt-1 text-stone-600">{when(submission.submitted_at)}</p></div><span class="shrink-0 {submission.correct ? 'text-up' : 'text-down'}">{submission.correct ? 'correct' : 'wrong'}</span></div>{/each}</div>{:else}<EmptyState icon="mdi:form-textbox-password" text="No submissions." />{/if}
					</Card></div>

				{:else if tab === 'access'}
					<div class="grid grid-cols-2 gap-3 md:grid-cols-4">
						{#each [
							{ label: 'Sign-ins', value: detail.access_summary?.login_count ?? 0 },
							{ label: 'Active sessions', value: detail.access_summary?.active_sessions ?? 0 },
							{ label: 'IP addresses', value: detail.access_ips?.length ?? 0 },
							{ label: 'First sign in', value: detail.access_summary?.first_login_at ? when(detail.access_summary.first_login_at) : 'Never' }
						] as item}
							<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-3"><p class="metadata-label text-stone-600">{item.label}</p><p class="mt-1 text-sm font-medium tabular-nums text-stone-100">{item.value}</p></div>
						{/each}
					</div>
					<div class="mt-4 grid items-start gap-4 xl:grid-cols-3">
						<Card title={`Sign-in history (${detail.login_history?.length ?? 0})`} bodyClass="p-0">
							{#if detail.login_history?.length}<div class="max-h-96 divide-y divide-stone-800/60 overflow-y-auto">{#each detail.login_history as login}<div class="px-4 py-3 text-xs"><div class="flex items-center justify-between gap-3"><p class="font-mono text-stone-300">{login.ip_address || 'IP not retained'}</p><span class="text-stone-600">{when(login.logged_in_at)}</span></div><p class="mt-1 truncate text-stone-600" title={login.user_agent}>{login.user_agent || 'Client not recorded'}</p></div>{/each}</div>{:else}<EmptyState icon="mdi:login-variant" text="This account has not signed in." />{/if}
						</Card>
						<Card title={`IP activity (${detail.access_ips?.length ?? 0})`} bodyClass="p-0">
							{#if detail.access_ips?.length}<div class="max-h-96 divide-y divide-stone-800/60 overflow-y-auto">{#each detail.access_ips as row}<div class="px-4 py-3 text-xs"><div class="flex items-center justify-between gap-3"><p class="font-mono text-stone-300">{row.ip_address}</p><span class="tabular-nums text-stone-600">{row.events}</span></div><p class="mt-1 text-stone-600">{row.sources?.join(', ')}</p><p class="mt-1 text-stone-600">{when(row.first_seen_at)} → {when(row.last_seen_at)}</p></div>{/each}</div>{:else}<EmptyState icon="mdi:ip-network-outline" text="No IP activity recorded." />{/if}
						</Card>
						<Card title={`Access sessions (${detail.auth_sessions?.length ?? 0})`} bodyClass="p-0">
							{#if detail.auth_sessions?.length}<div class="max-h-96 divide-y divide-stone-800/60 overflow-y-auto">{#each detail.auth_sessions as session}<div class="px-4 py-3 text-xs"><div class="flex items-center justify-between gap-3"><p class="text-stone-300">Started {when(session.created_at)}</p><span class={session.active ? 'text-up' : 'text-stone-600'}>{session.active ? 'active' : session.revoked ? 'revoked' : 'expired'}</span></div><p class="mt-1 text-stone-600">Expires {when(session.expires_at)}</p></div>{/each}</div>{:else}<EmptyState icon="mdi:devices" text="No access sessions." />{/if}
						</Card>
					</div>

				{:else if tab === 'moderation'}
					<div class="grid items-start gap-4 lg:grid-cols-2">
						<Card title={`Warnings (${detail.warnings?.length ?? 0})`} bodyClass="p-0">
							{#if detail.warnings?.length}<div class="max-h-[32rem] divide-y divide-stone-800/60 overflow-y-auto">{#each detail.warnings as warning}<div class="px-4 py-3 text-xs"><div class="flex flex-wrap items-center justify-between gap-2"><span class="font-medium text-warn">{warning.cancelled_at ? 'Cancelled' : warning.dismissed_at ? 'Dismissed' : warning.read_at ? 'Read' : 'Unread'}</span><span class="text-stone-600">{when(warning.published_at)}</span></div><p class="mt-2 whitespace-pre-wrap break-words text-stone-300">{warning.body}</p><p class="mt-2 text-stone-600">{warning.actor || 'System'}{warning.pinned ? ' · pinned' : ''}</p></div>{/each}</div>{:else}<EmptyState icon="mdi:message-alert-outline" text="No warnings." />{/if}
						</Card>
						<Card title={`Admin history (${administrativeHistory.length})`} bodyClass="p-0">
							{#if administrativeHistory.length}<div class="max-h-[32rem] divide-y divide-stone-800/60 overflow-y-auto">{#each administrativeHistory as event}<div class="px-4 py-3 text-xs"><div class="flex flex-wrap items-center justify-between gap-2"><p class="font-medium text-stone-300">{event.action.replaceAll('_', ' ').replaceAll('.', ' ')}</p><span class="text-stone-600">{when(event.created_at)}</span></div><p class="mt-1 text-stone-600">{event.actor || 'System'} · {event.ip_address || 'IP not retained'}</p>{#if event.new_values}<pre class="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded bg-stone-950 p-2 font-mono text-[10px] text-stone-500">{JSON.stringify(event.new_values, null, 2)}</pre>{/if}</div>{/each}</div>{:else}<EmptyState icon="mdi:clipboard-text-clock-outline" text="No administrative changes." />{/if}
						</Card>
					</div>
				{/if}
			{/if}
		</div>
	</div>
</div>
