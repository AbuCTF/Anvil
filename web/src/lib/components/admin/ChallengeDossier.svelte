<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import Icon from '@iconify/svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { difficultyClass } from '$lib/rank';
	import { formatLocalDateTimeWithZone, instantTitle } from '$lib/time';

	export let seed: any;
	export let detail: any = null;
	export let loading = false;
	export let error = '';
	export let economyEnabled = false;

	const dispatch = createEventDispatcher();
	let tab = 'overview';
	$: stats = detail?.stats ?? {};
	$: tabs = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'submissions', label: 'Submissions', count: stats.submissions ?? 0 },
		{ id: 'solves', label: 'Solves', count: detail?.solves?.length ?? 0 },
		{ id: 'instances', label: 'Instances', count: stats.instances ?? 0 },
		...(economyEnabled ? [{ id: 'economy', label: 'Ledger state', count: detail?.economy?.length ?? 0 }] : []),
		{ id: 'audit', label: 'Admin history', count: detail?.audit?.length ?? 0 }
	];

	function when(value: number | undefined) {
		return value ? formatLocalDateTimeWithZone(value, 'seconds') : '—';
	}

	function statusClass(value: string) {
		if (['published', 'running', 'solved'].includes(value)) return 'text-up';
		if (['failed', 'wrong', 'expired'].includes(value)) return 'text-down';
		if (['draft', 'open', 'creating', 'pending', 'stopping'].includes(value)) return 'text-warn';
		return 'text-stone-500';
	}
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center p-2 sm:p-4">
	<button type="button" aria-label="Close challenge details" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => dispatch('close')}></button>
	<div class="relative z-10 flex max-h-[96vh] w-full max-w-7xl flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-950 shadow-2xl shadow-black/30" role="dialog" aria-modal="true" aria-label="Challenge details">
		<header class="flex items-start justify-between gap-4 border-b border-stone-800 px-4 py-4 sm:px-6">
			<div class="min-w-0">
				<div class="flex flex-wrap items-center gap-2">
					<p class="metadata-label text-stone-500">Challenge dossier</p>
					<span class="rounded border px-1.5 py-0.5 text-[10px] capitalize {difficultyClass(seed.difficulty)}">{seed.difficulty}</span>
					<span class="text-[10px] capitalize {statusClass(seed.status)}">{seed.status}</span>
				</div>
				<h2 class="mt-1 truncate text-xl font-semibold text-stone-100">{seed.name}</h2>
				<p class="mt-1 font-mono text-xs text-stone-600">{seed.slug} · {seed.id}</p>
			</div>
			<div class="flex shrink-0 items-center gap-3">
				<a href="/challenges/{seed.slug}" target="_blank" rel="noreferrer" class="hidden items-center gap-1 text-xs text-stone-400 hover:text-stone-200 sm:inline-flex"><Icon icon="mdi:open-in-new" class="h-3.5 w-3.5" />{seed.status === 'draft' ? 'Admin preview' : 'Player view'}</a>
				<button type="button" on:click={() => dispatch('edit')} class="text-xs text-amber-500 hover:text-amber-400">Edit</button>
				<button type="button" on:click={() => dispatch('close')} class="rounded p-1 text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button>
			</div>
		</header>

		<nav class="flex shrink-0 gap-1 overflow-x-auto border-b border-stone-800 px-3 sm:px-6" aria-label="Challenge dossier sections">
			{#each tabs as item}
				<button type="button" on:click={() => (tab = item.id)} class="shrink-0 border-b-2 px-3 py-3 text-xs font-medium transition-colors {tab === item.id ? 'border-amber-500 text-stone-100' : 'border-transparent text-stone-500 hover:text-stone-300'}">{item.label}{#if item.count !== undefined}<span class="ml-1.5 tabular-nums text-stone-600">{item.count}</span>{/if}</button>
			{/each}
		</nav>

		<div class="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
			{#if loading}
				<div class="grid min-h-72 place-items-center"><Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-600" /></div>
			{:else if error}
				<div class="rounded-md border border-down/30 bg-down/10 p-4 text-sm text-down">{error}</div>
			{:else if detail}
				{#if tab === 'overview'}
					<div class="grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-8">
						{#each [
							{ label: 'Base points', value: seed.base_points ?? 0, accent: true },
							{ label: 'Teams solved', value: stats.solved_teams ?? seed.economy_solve_count ?? 0 },
							{ label: 'Open teams', value: stats.open_teams ?? 0 },
							{ label: 'Attempts', value: stats.submissions ?? seed.total_attempts ?? 0 },
							{ label: 'Wrong', value: stats.wrong_submissions ?? 0 },
							{ label: 'Instances', value: stats.instances ?? 0 },
							{ label: 'Files', value: stats.attachments ?? 0 },
							{ label: 'Credits spent', value: Number(stats.credits_spent ?? 0).toLocaleString(undefined, { maximumFractionDigits: 3 }) }
						] as item}
							<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-3"><p class="metadata-label text-stone-600">{item.label}</p><p class="mt-1 text-xl font-semibold tabular-nums {item.accent ? 'text-amber-500' : 'text-stone-100'}">{item.value}</p></div>
						{/each}
					</div>

					<div class="mt-5 grid items-start gap-4 lg:grid-cols-2">
						<Card title="Configuration" bodyClass="p-4">
							<dl class="grid grid-cols-2 gap-x-5 gap-y-3 text-xs sm:grid-cols-3">
								<div><dt class="metadata-label text-stone-600">Delivery</dt><dd class="mt-1 capitalize text-stone-300">{seed.delivery_type?.replace('_', ' ') || seed.resource_type}</dd></div>
								<div><dt class="metadata-label text-stone-600">Scoring</dt><dd class="mt-1 capitalize text-stone-300">{seed.scoring_mode || 'flag'}</dd></div>
								<div><dt class="metadata-label text-stone-600">Topology</dt><dd class="mt-1 text-stone-300">{seed.arena_mode === 'shared' ? 'Shared arena' : 'Per team'}</dd></div>
								<div><dt class="metadata-label text-stone-600">Category</dt><dd class="mt-1 text-stone-300">{seed.category_name || 'Uncategorised'}</dd></div>
								<div><dt class="metadata-label text-stone-600">Author</dt><dd class="mt-1 text-stone-300">{seed.author_name || '—'}</dd></div>
								<div><dt class="metadata-label text-stone-600">Privileged path</dt><dd class="mt-1 text-stone-300">{seed.privesc ? 'SUID / privesc enabled' : 'Hardened'}</dd></div>
								<div><dt class="metadata-label text-stone-600">Timeout</dt><dd class="mt-1 text-stone-300">{seed.resource_type === 'vm' ? seed.vm_timeout_minutes : seed.instance_timeout} min</dd></div>
								<div><dt class="metadata-label text-stone-600">Extensions</dt><dd class="mt-1 text-stone-300">{seed.resource_type === 'vm' ? seed.vm_max_extensions : seed.max_extensions}</dd></div>
								<div><dt class="metadata-label text-stone-600">Created</dt><dd class="mt-1 text-stone-300">{when(seed.created_at)}</dd></div>
							</dl>
							{#if seed.sub_description}<div class="mt-4 border-t border-stone-800 pt-3"><p class="metadata-label text-stone-600">Pre-launch summary</p><p class="mt-1 text-sm leading-relaxed text-stone-400">{seed.sub_description}</p></div>{/if}
						</Card>

						<Card title="Runtime" bodyClass="p-4">
							{#if seed.delivery_type === 'static'}
								<p class="text-sm text-stone-400">Download or flag-only challenge. No runtime is provisioned.</p>
							{:else if seed.delivery_type === 'external'}
								<p class="text-sm text-stone-400">External target or OSINT challenge. Anvil serves the instructions and scoring flow without provisioning a runtime.</p>
							{:else if seed.resource_type === 'vm'}
								<p class="text-sm text-stone-300">VM template <span class="font-mono text-stone-500">{seed.vm_template_id || 'not selected'}</span></p>
							{:else if seed.delivery_type === 'multi'}
								<div class="space-y-2">{#each seed.services ?? [] as service}<div class="rounded-md border border-stone-800 bg-stone-950 p-3 text-xs"><div class="flex items-center justify-between gap-3"><span class="font-medium text-stone-300">{service.name}</span><span class="text-stone-600">{service.public ? 'Public' : 'Internal'}{service.egress ? ' · egress' : ''}</span></div><p class="mt-1 break-all font-mono text-stone-500">{service.image || seed.container_image}:{service.tag || seed.container_tag || 'latest'}</p><p class="mt-1 text-stone-600">{service.cpu_limit || 'default'} CPU · {service.memory_limit || 'default'} · {(service.ports || []).map((port: any) => `${port.port}/${port.service || port.protocol}`).join(', ') || 'no ports'}</p></div>{/each}</div>
							{:else}
								<p class="break-all font-mono text-sm text-stone-300">{seed.container_image}:{seed.container_tag || 'latest'}</p><p class="mt-2 text-xs text-stone-600">{seed.container_platform || 'native platform'} · {seed.cpu_limit || 'default'} CPU · {seed.memory_limit || 'default'} · {(seed.exposed_ports || []).map((port: any) => `${port.port}/${port.service || port.protocol}`).join(', ') || 'no public ports'}</p>
							{/if}
						</Card>
					</div>

					<div class="mt-4 grid items-start gap-4 lg:grid-cols-3">
						<Card title={`Flags (${detail.flags?.length ?? 0})`} bodyClass="p-0"><div class="divide-y divide-stone-800/60">{#each detail.flags ?? [] as flag}<div class="flex items-center justify-between gap-3 px-4 py-3 text-xs"><div><p class="text-stone-300">{flag.name}</p><p class="mt-1 text-stone-600">{flag.flag_type || 'static'}{flag.dynamic_flag_prefix ? ` · ${flag.dynamic_flag_prefix}{uuid}` : ''}</p></div><span class="tabular-nums text-stone-400">{flag.points}</span></div>{/each}{#if !detail.flags?.length}<EmptyState icon="mdi:flag-outline" text="No flags." />{/if}</div></Card>
						<Card title={`Hints (${detail.hints?.length ?? 0})`} bodyClass="p-0"><div class="divide-y divide-stone-800/60">{#each detail.hints ?? [] as hint}<div class="px-4 py-3 text-xs"><p class="line-clamp-3 text-stone-400">{hint.content}</p><p class="mt-1 text-stone-600">{hint.cost || 0} credits</p></div>{/each}{#if !detail.hints?.length}<EmptyState icon="mdi:lightbulb-outline" text="No hints." />{/if}</div></Card>
						<Card title={`Handouts (${detail.attachments?.length ?? 0})`} bodyClass="p-0"><div class="divide-y divide-stone-800/60">{#each detail.attachments ?? [] as file}<div class="px-4 py-3 text-xs"><div class="flex items-center justify-between gap-3"><p class="truncate text-stone-300">{file.filename}</p><span class="shrink-0 text-stone-600">{file.url ? 'External' : 'Managed'}</span></div><p class="mt-1 truncate font-mono text-stone-600" title={file.sha256}>{file.sha256 ? `sha256:${file.sha256}` : 'Checksum unavailable'} · {file.file_size || 0} bytes</p></div>{/each}{#if !detail.attachments?.length}<EmptyState icon="mdi:paperclip" text="No handouts." />{/if}</div></Card>
					</div>
				{:else if tab === 'submissions'}
					<div class="mb-3 rounded-md border border-stone-800 bg-stone-900/30 px-3 py-2 text-xs text-stone-500">The audit view uses irreversible fingerprints rather than redistributing valid flag material.</div>
					{#if detail.submissions?.length}
						<div class="overflow-x-auto rounded-lg border border-stone-800"><table class="w-full min-w-[960px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Participant</th><th class="px-3 py-2 text-left">Team</th><th class="px-3 py-2 text-left">Flag</th><th class="px-3 py-2 text-left">Result</th><th class="px-3 py-2 text-left">Fingerprint</th><th class="px-3 py-2 text-left">IP</th><th class="px-3 py-2 text-left">Client</th><th class="px-3 py-2 text-right">When</th></tr></thead><tbody>{#each detail.submissions as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.username || 'Session'}</td><td class="px-3 py-2 text-stone-400">{row.team_name || '—'}</td><td class="px-3 py-2 text-stone-400">{row.flag_name || 'Unknown'}</td><td class="px-3 py-2 {row.correct ? 'text-up' : 'text-down'}">{row.correct ? 'Correct' : 'Wrong'}</td><td class="px-3 py-2 font-mono text-stone-500">{row.flag_fingerprint} · {row.flag_length}</td><td class="px-3 py-2 font-mono text-stone-400">{row.ip_address || '—'}</td><td class="max-w-64 truncate px-3 py-2 text-stone-600" title={row.user_agent}>{row.user_agent || '—'}</td><td class="px-3 py-2 text-right text-stone-600" title={instantTitle(row.submitted_at, 'seconds')}>{when(row.submitted_at)}</td></tr>{/each}</tbody></table></div>
					{:else}<EmptyState icon="mdi:form-textbox-password" text="No submissions." />{/if}
				{:else if tab === 'solves'}
					{#if detail.solves?.length}<div class="overflow-x-auto rounded-lg border border-stone-800"><table class="w-full min-w-[680px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Participant</th><th class="px-3 py-2 text-left">Team</th><th class="px-3 py-2 text-left">Flag</th><th class="px-3 py-2 text-right">Points</th><th class="px-3 py-2 text-right">When</th></tr></thead><tbody>{#each detail.solves as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.username || '—'}</td><td class="px-3 py-2 text-stone-400">{row.team_name || '—'}</td><td class="px-3 py-2 text-stone-400">{row.flag_name || '—'}</td><td class="px-3 py-2 text-right tabular-nums text-up">+{row.points}</td><td class="px-3 py-2 text-right text-stone-600">{when(row.solved_at)}</td></tr>{/each}</tbody></table></div>{:else}<EmptyState icon="mdi:flag-checkered" text="No solves." />{/if}
				{:else if tab === 'instances'}
					{#if detail.instances?.length}<div class="space-y-2">{#each detail.instances as row}<div class="rounded-lg border border-stone-800 bg-stone-900/30 p-3 text-xs"><div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="text-stone-300">{row.team_name || 'No team'} · {row.username || 'Unknown owner'}</p><p class="mt-1 break-all font-mono text-stone-600">{row.id}{row.target ? ` · ${row.target}` : ''}</p></div><span class="shrink-0 {statusClass(row.status)}">{row.status}</span></div><div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-stone-600"><span>Created {when(row.created_at)}</span><span>{row.extensions_used} extensions</span><span>{row.reset_count} resets</span>{#if row.stopped_at}<span>Stopped {when(row.stopped_at)}</span>{/if}</div>{#if row.error_message}<p class="mt-2 text-down">{row.error_message}</p>{/if}</div>{/each}</div>{:else}<EmptyState icon="mdi:cube-off-outline" text="No instance history." />{/if}
				{:else if tab === 'economy'}
					{#if detail.economy?.length}<div class="overflow-x-auto rounded-lg border border-stone-800"><table class="w-full min-w-[820px] text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-3 py-2 text-left">Team</th><th class="px-3 py-2 text-left">State</th><th class="px-3 py-2 text-right">Value</th><th class="px-3 py-2 text-right">Fraction</th><th class="px-3 py-2 text-right">Wrong</th><th class="px-3 py-2 text-right">Extensions</th><th class="px-3 py-2 text-right">Opened</th><th class="px-3 py-2 text-right">Expires</th></tr></thead><tbody>{#each detail.economy as row}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-3 py-2 text-stone-300">{row.team_name}</td><td class="px-3 py-2 {statusClass(row.status)}">{row.status}{row.holds_solve ? ' · held' : ''}</td><td class="px-3 py-2 text-right tabular-nums text-stone-300">{Number(row.current_value).toFixed(3)}</td><td class="px-3 py-2 text-right tabular-nums text-stone-400">{(Number(row.fraction) * 100).toFixed(1)}%</td><td class="px-3 py-2 text-right text-stone-400">{row.wrong_submissions}</td><td class="px-3 py-2 text-right text-stone-400">{row.extensions_used}</td><td class="px-3 py-2 text-right text-stone-600">{when(row.opened_at)}</td><td class="px-3 py-2 text-right text-stone-600">{when(row.expires_at)}</td></tr>{/each}</tbody></table></div>{:else}<EmptyState icon="mdi:book-open-variant" text="No Ledger state." />{/if}
				{:else if tab === 'audit'}
					{#if detail.audit?.length}<div class="space-y-2">{#each detail.audit as row}<div class="rounded-lg border border-stone-800 bg-stone-900/30 p-3 text-xs"><div class="flex flex-wrap items-center justify-between gap-2"><p class="font-medium text-stone-300">{row.action.replaceAll('_', ' ')}</p><span class="text-stone-600">{when(row.created_at)}</span></div><p class="mt-1 text-stone-600">{row.actor || 'System'} · {row.ip_address || 'No IP'}</p>{#if row.new_values}<pre class="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded bg-stone-950 p-2 font-mono text-[10px] text-stone-500">{JSON.stringify(row.new_values, null, 2)}</pre>{/if}</div>{/each}</div>{:else}<EmptyState icon="mdi:clipboard-text-clock-outline" text="No administrative changes recorded." />{/if}
				{/if}
			{/if}
		</div>
	</div>
</div>
