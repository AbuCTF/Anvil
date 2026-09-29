<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type ReadinessCheck, type ReadinessReport, type ReleaseCandidate } from '$api';

	let report: ReadinessReport | null = null;
	let releases: ReleaseCandidate[] = [];
	let waived = new Set<string>();
	let loading = true;
	let sealing = false;
	let error = '';
	let message = '';

	$: groups = report ? [...new Set(report.checks.map((check) => check.group))] : [];
	$: warningIDs = report?.checks.filter((check) => check.status === 'warning').map((check) => check.id) ?? [];
	$: everyWarningWaived = warningIDs.every((id) => waived.has(id));

	async function load() {
		loading = true;
		error = '';
		try {
			const [nextReport, history] = await Promise.all([api.getReadiness(), api.getReleaseCandidates()]);
			report = nextReport;
			releases = history.release_candidates;
			waived = new Set([...waived].filter((id) => warningIDs.includes(id)));
		} catch (e) {
			error = e instanceof Error ? e.message : 'Release readiness is unavailable';
		} finally {
			loading = false;
		}
	}

	function toggleWaiver(id: string) {
		const next = new Set(waived);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		waived = next;
	}

	async function seal() {
		if (!report?.ready || !everyWarningWaived) return;
		sealing = true;
		error = '';
		message = '';
		try {
			const created = await api.createReleaseCandidate([...waived]);
			message = `Release candidate RC-${created.sequence} sealed with immutable configuration fingerprints.`;
			waived = new Set();
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not seal release candidate';
		} finally {
			sealing = false;
		}
	}

	function statusIcon(check: ReadinessCheck) {
		if (check.status === 'pass') return 'mdi:check-circle';
		if (check.status === 'warning') return 'mdi:alert-circle';
		return 'mdi:close-circle';
	}

	function short(value: string) {
		return value.slice(0, 12);
	}

	onMount(load);
</script>

<div class="space-y-6">
	<div><h2 class="text-base font-semibold text-stone-100">Release control</h2><p class="mt-1 max-w-3xl text-xs leading-relaxed text-stone-500">Run the final event, content, runtime, access and Ledger checks, review each warning, then seal an auditable release candidate. Sealing records exactly what staff approved; it never deploys infrastructure or publishes challenges.</p></div>
	{#if loading}
		<div class="flex min-h-[18rem] items-center justify-center"><Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-stone-600" /></div>
	{:else}
		{#if error}<div class="rounded-md border border-down/20 bg-down/5 px-4 py-3 text-sm text-down">{error}</div>{/if}
		{#if message}<div class="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-4 py-3 text-sm text-emerald-400">{message}</div>{/if}

		{#if report}
			<section class="overflow-hidden rounded-lg border {report.ready ? 'border-emerald-500/20' : 'border-down/20'} bg-stone-900/25">
				<div class="flex flex-col gap-5 p-5 sm:flex-row sm:items-center sm:justify-between">
					<div class="flex items-center gap-4">
						<div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-full {report.ready ? 'bg-emerald-500/10 text-emerald-400' : 'bg-down/10 text-down'}"><Icon icon={report.ready ? 'mdi:shield-check-outline' : 'mdi:shield-alert-outline'} class="h-6 w-6" /></div>
						<div><p class="text-base font-semibold text-stone-100">{report.ready ? 'Ready for release review' : `${report.blockers} release blocker${report.blockers === 1 ? '' : 's'}`}</p><p class="mt-1 text-xs text-stone-500">Live checks generated {new Date(report.generated_at).toLocaleString()}</p></div>
					</div>
					<button type="button" on:click={load} class="inline-flex items-center justify-center gap-2 rounded-md border border-stone-700 px-3 py-2 text-xs text-stone-300 hover:border-stone-600"><Icon icon="mdi:refresh" class="h-4 w-4" />Run checks again</button>
				</div>
				<div class="grid grid-cols-2 border-t border-stone-800 sm:grid-cols-4">
					<div class="border-r border-stone-800 p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">Checks</p><p class="mt-1 text-xl font-semibold text-stone-200">{report.checks.length}</p></div>
					<div class="border-r border-stone-800 p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">Blockers</p><p class="mt-1 text-xl font-semibold {report.blockers ? 'text-down' : 'text-emerald-400'}">{report.blockers}</p></div>
					<div class="border-r border-stone-800 p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">Warnings</p><p class="mt-1 text-xl font-semibold {report.warnings ? 'text-amber-400' : 'text-emerald-400'}">{report.warnings}</p></div>
					<div class="p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">Latest RC</p><p class="mt-1 text-xl font-semibold text-stone-200">{releases.length ? `#${releases[0].sequence}` : 'None'}</p></div>
				</div>
			</section>

			<div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
				<div class="space-y-4">
					{#each groups as group}
						<section class="overflow-hidden rounded-lg border border-stone-800 bg-stone-900/25">
							<div class="border-b border-stone-800 px-5 py-3"><h2 class="text-xs font-semibold uppercase tracking-wider text-stone-500">{group}</h2></div>
							<div class="divide-y divide-stone-800/70">
								{#each report.checks.filter((check) => check.group === group) as check}
									<div class="flex items-start gap-3 px-5 py-4">
										<Icon icon={statusIcon(check)} class="mt-0.5 h-4 w-4 shrink-0 {check.status === 'pass' ? 'text-emerald-400' : check.status === 'warning' ? 'text-amber-400' : 'text-down'}" />
										<div class="min-w-0 flex-1"><div class="flex flex-wrap items-center justify-between gap-2"><p class="text-sm font-medium text-stone-200">{check.label}</p><span class="text-[10px] uppercase tracking-wider {check.status === 'pass' ? 'text-emerald-400' : check.status === 'warning' ? 'text-amber-400' : 'text-down'}">{check.status}</span></div><p class="mt-1 text-xs leading-relaxed text-stone-500">{check.detail}</p></div>
										{#if check.status === 'warning'}<label class="flex shrink-0 cursor-pointer items-center gap-2 text-[10px] uppercase tracking-wider text-stone-500"><input type="checkbox" checked={waived.has(check.id)} on:change={() => toggleWaiver(check.id)} class="accent-amber-500" />Waive</label>{/if}
									</div>
								{/each}
							</div>
						</section>
					{/each}
				</div>

				<div class="space-y-4">
					<section class="rounded-lg border border-stone-800 bg-stone-900/25 p-5 xl:sticky xl:top-5">
						<h2 class="text-sm font-semibold text-stone-100">Seal release candidate</h2>
						<p class="mt-2 text-xs leading-relaxed text-stone-500">Capture immutable fingerprints for event settings, challenge content and the Ledger policy. This records approval; it does not deploy or publish the event.</p>
						<div class="mt-4 space-y-2 rounded-md border border-stone-800 bg-stone-950/50 p-3 font-mono text-[10px] text-stone-500"><p>event&nbsp;&nbsp;&nbsp; {short(report.event_checksum)}</p><p>content&nbsp; {short(report.content_checksum)}</p><p>economy&nbsp; {short(report.economy_checksum)}</p></div>
						{#if report.warnings}<p class="mt-4 text-xs leading-relaxed text-amber-400">Review and waive each warning individually. Waivers are stored in the release record.</p>{/if}
						<button type="button" on:click={seal} disabled={!report.ready || !everyWarningWaived || sealing} class="mt-4 inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:cursor-not-allowed disabled:opacity-35"><Icon icon={sealing ? 'mdi:loading' : 'mdi:seal-variant'} class="h-4 w-4 {sealing ? 'animate-spin' : ''}" />Seal candidate</button>
					</section>
				</div>
			</div>

			<section class="overflow-hidden rounded-lg border border-stone-800 bg-stone-900/25">
				<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Release history</h2><p class="mt-1 text-xs text-stone-500">Each record binds the exact configuration, content and economy contract reviewed at that moment.</p></div>
				{#if releases.length}<div class="divide-y divide-stone-800/70">{#each releases as release}<div class="grid gap-2 px-5 py-4 text-xs sm:grid-cols-[5rem_minmax(0,1fr)_auto] sm:items-center"><span class="font-semibold text-stone-200">RC-{release.sequence}</span><span class="font-mono text-[10px] text-stone-600">{short(release.event_checksum)} / {short(release.content_checksum)} / {short(release.economy_checksum)}</span><span class="text-stone-500">{release.created_by} · {new Date(release.created_at).toLocaleString()}</span></div>{/each}</div>{:else}<div class="px-5 py-10 text-center text-sm text-stone-600">No release candidate has been sealed.</div>{/if}
			</section>
		{/if}
	{/if}
</div>
