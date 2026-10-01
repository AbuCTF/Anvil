<script lang="ts">
	import Icon from '@iconify/svelte';
	import { api, type DataImportPreview, type DiscoveredRegistryRepository } from '$api';
	import RegistryCredentials from '$lib/components/admin/RegistryCredentials.svelte';

	export let categories: any[] = [];
	export let onClose: () => void;
	export let onApplied: () => Promise<void>;

	let registry: 'docker.io' | 'ghcr.io' = 'docker.io';
	let namespace = '';
	let repositories: DiscoveredRegistryRepository[] = [];
	let selected = new Set<string>();
	let slugs: Record<string, string> = {};
	let search = '';
	let credentialUsed = false;
	let truncated = false;
	let categorySlug = '';
	let difficulty = 'medium';
	let basePoints = 100;
	let authorName = '';
	let discovering = false;
	let applying = false;
	let preview: DataImportPreview | null = null;
	let error = '';
	let complete = '';

	const field = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-500';
	const label = 'mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-stone-600';
	$: visibleRepositories = repositories.filter((repository) => !search || `${repository.name} ${repository.image}`.toLowerCase().includes(search.toLowerCase()));
	$: selectedRepositories = repositories.filter((repository) => selected.has(repository.image));

	function toggle(image: string) {
		const next = new Set(selected);
		if (next.has(image)) next.delete(image);
		else next.add(image);
		selected = next;
		preview = null;
	}

	function selectAvailable() {
		selected = new Set(repositories.filter((repository) => !repository.already_imported && !repository.slug_conflict).map((repository) => repository.image));
		preview = null;
	}

	function challengeName(name: string) {
		return name.split('/').pop()?.replace(/[-_.]+/g, ' ').replace(/\b\w/g, (character) => character.toUpperCase()) || name;
	}

	async function discover() {
		if (!namespace.trim()) return;
		discovering = true;
		error = '';
		complete = '';
		preview = null;
		try {
			const result = await api.discoverRegistryRepositories(registry, namespace.trim());
			repositories = result.repositories;
			credentialUsed = result.credential_used;
			truncated = result.truncated;
			slugs = Object.fromEntries(result.repositories.map((repository) => [repository.image, repository.suggested_slug]));
			selectAvailable();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Repository discovery failed';
		} finally {
			discovering = false;
		}
	}

	function importRows() {
		return selectedRepositories.map((repository) => ({
			slug: slugs[repository.image]?.trim() || repository.suggested_slug,
			name: challengeName(repository.name),
			description: repository.description || '',
			difficulty,
			category_slug: categorySlug,
			status: 'draft',
			author_name: authorName.trim(),
			resource_type: 'docker',
			delivery_type: 'docker',
			container_image: repository.image.replace(/:latest$/, ''),
			container_tag: 'latest',
			cpu_limit: '1',
			memory_limit: '512m',
			exposed_ports: '[]',
			base_points: String(basePoints),
			privesc: 'false',
			scoring_mode: 'flag'
		}));
	}

	async function dryRun() {
		if (!selectedRepositories.length) return;
		applying = true;
		error = '';
		complete = '';
		try {
			preview = await api.previewDataImport({
				entity: 'challenges', format: 'json', mode: 'create',
				source_name: `${registry}-${namespace.trim()}-repositories.json`,
				content: JSON.stringify(importRows())
			});
		} catch (e) {
			error = e instanceof Error ? e.message : 'Import preview failed';
		} finally {
			applying = false;
		}
	}

	async function apply() {
		if (!preview || preview.plan.errors.length) return;
		applying = true;
		error = '';
		try {
			const result = await api.applyDataImport(preview.job_id, preview.checksum);
			complete = `${result.created} challenge ${result.created === 1 ? 'draft' : 'drafts'} imported.`;
			for (const repository of selectedRepositories) repository.already_imported = true;
			selected = new Set();
			preview = null;
			await onApplied();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Import failed';
		} finally {
			applying = false;
		}
	}
</script>

<div class="fixed inset-0 z-[60] flex items-center justify-center p-3 sm:p-5">
	<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/85 backdrop-blur-sm" on:click={onClose}></button>
	<div class="relative z-10 flex max-h-[94vh] w-full max-w-6xl flex-col overflow-hidden rounded-xl border border-stone-800 bg-stone-950 shadow-2xl" role="dialog" aria-modal="true" aria-label="Import registry collection">
		<div class="flex items-center justify-between gap-4 border-b border-stone-800 px-5 py-4 sm:px-6"><div><h2 class="text-base font-semibold text-stone-100">Import registry collection</h2><p class="mt-1 text-xs text-stone-500">Discover a namespace, choose repositories, then import reviewed drafts in one transaction.</p></div><button type="button" class="p-1 text-stone-500 hover:text-stone-200" on:click={onClose}><Icon icon="mdi:close" class="h-5 w-5" /></button></div>
		<div class="grid min-h-0 flex-1 overflow-y-auto lg:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.75fr)] lg:overflow-hidden">
			<div class="space-y-4 p-5 sm:p-6 lg:overflow-y-auto">
				<div class="grid gap-3 sm:grid-cols-[170px_minmax(0,1fr)_auto]"><label><span class={label}>Registry</span><select class={field} bind:value={registry} on:change={() => { repositories = []; selected = new Set(); preview = null; }}><option value="docker.io">Docker Hub</option><option value="ghcr.io">GHCR</option></select></label><label><span class={label}>{registry === 'docker.io' ? 'Namespace' : 'User or organization'}</span><input class={field} bind:value={namespace} placeholder={registry === 'docker.io' ? 'organization' : 'github-owner'} /></label><button type="button" class="self-end rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40" disabled={discovering || !namespace.trim()} on:click={discover}>{discovering ? 'Discovering…' : 'Discover'}</button></div>
				<RegistryCredentials />
				{#if error}<div class="rounded-md border border-down/20 bg-down/5 px-3 py-2.5 text-sm text-down">{error}</div>{/if}
				{#if complete}<div class="rounded-md border border-up/20 bg-up/5 px-3 py-2.5 text-sm text-up">{complete}</div>{/if}
				{#if repositories.length}
					<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between"><div class="flex items-center gap-3"><button type="button" class="text-xs text-stone-300 hover:text-stone-100" on:click={selectAvailable}>Select available</button><button type="button" class="text-xs text-stone-500 hover:text-stone-200" on:click={() => { selected = new Set(); preview = null; }}>Clear</button><span class="text-xs tabular-nums text-stone-600">{selected.size} of {repositories.length}</span></div><div class="relative"><Icon icon="mdi:magnify" class="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-stone-600" /><input type="search" class="rounded-md border border-stone-800 bg-stone-950 py-2 pl-8 pr-3 text-xs text-stone-300 outline-none focus:border-stone-600" bind:value={search} placeholder="Filter repositories" /></div></div>
					<div class="max-h-[48vh] divide-y divide-stone-800/70 overflow-auto rounded-lg border border-stone-800">
						{#each visibleRepositories as repository}
							<label class="flex gap-3 px-3 py-3 {repository.already_imported ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-stone-900/50'}"><input type="checkbox" class="mt-1 h-4 w-4 shrink-0 accent-amber-500" disabled={repository.already_imported} checked={selected.has(repository.image)} on:change={() => toggle(repository.image)} /><span class="min-w-0 flex-1"><span class="flex flex-wrap items-center gap-2"><span class="truncate text-sm font-medium text-stone-200">{repository.name}</span><span class="rounded-full border border-stone-800 px-1.5 py-0.5 text-[9px] uppercase text-stone-600">{repository.visibility}</span>{#if repository.already_imported}<span class="text-[10px] text-up">Imported</span>{:else if repository.slug_conflict}<span class="text-[10px] text-warn">Slug needs editing</span>{/if}</span><span class="mt-1 block truncate font-mono text-[11px] text-stone-600">{repository.image}</span>{#if selected.has(repository.image)}<span class="mt-2 grid gap-1"><span class="text-[9px] uppercase tracking-wider text-stone-600">Challenge slug</span><input class="rounded border border-stone-800 bg-stone-950 px-2 py-1.5 font-mono text-xs text-stone-300 outline-none focus:border-stone-600" bind:value={slugs[repository.image]} on:click|stopPropagation on:input={() => preview = null} /></span>{/if}</span></label>
						{/each}
					</div>
					{#if truncated}<p class="text-[11px] text-warn">The first 500 repositories are shown. Narrow the namespace collection before importing more.</p>{/if}
					<p class="text-[11px] text-stone-600">{credentialUsed ? 'The saved encrypted credential was used, so accessible private repositories are included.' : registry === 'docker.io' ? 'Anonymous Docker Hub discovery shows public repositories only.' : 'Save a read-only GHCR credential to list GitHub container packages.'}</p>
				{:else if !discovering}<div class="rounded-lg border border-dashed border-stone-800 px-5 py-12 text-center"><Icon icon="mdi:package-variant-closed" class="mx-auto h-8 w-8 text-stone-700" /><p class="mt-3 text-sm text-stone-500">Enter the owner of the registry collection.</p></div>{/if}
			</div>
			<aside class="space-y-5 border-t border-stone-800 bg-stone-900/20 p-5 sm:p-6 lg:overflow-y-auto lg:border-l lg:border-t-0">
				<div><h3 class="text-sm font-semibold text-stone-200">Shared defaults</h3><p class="mt-1 text-xs leading-relaxed text-stone-600">Every imported challenge starts as a draft. Configure ports, flags, files, and immutable image pins before publishing.</p></div>
				<div class="space-y-3"><label><span class={label}>Category</span><select class={field} bind:value={categorySlug}><option value="">Uncategorized</option>{#each categories as category}<option value={category.slug}>{category.name}</option>{/each}</select></label><label><span class={label}>Difficulty</span><select class={field} bind:value={difficulty}><option value="easy">Easy</option><option value="medium">Medium</option><option value="hard">Hard</option><option value="insane">Insane</option></select></label><label><span class={label}>Base points</span><input type="number" min="1" max="1000000" class={field} bind:value={basePoints} /></label><label><span class={label}>Author</span><input class={field} maxlength="100" bind:value={authorName} placeholder="Optional" /></label></div>
				<button type="button" class="inline-flex w-full items-center justify-center gap-2 rounded-md border border-stone-700 px-4 py-2.5 text-sm font-medium text-stone-200 disabled:opacity-40" disabled={applying || !selected.size} on:click={dryRun}><Icon icon={applying ? 'mdi:loading' : 'mdi:magnify-scan'} class="h-4 w-4 {applying ? 'animate-spin' : ''}" />Dry-run {selected.size || ''} {selected.size === 1 ? 'repository' : 'repositories'}</button>
				{#if preview}<div class="rounded-lg border {preview.plan.errors.length ? 'border-down/20' : 'border-up/20'} bg-stone-950/60 p-4"><div class="flex items-center justify-between gap-3"><p class="text-sm font-medium text-stone-200">Import plan</p><span class="text-[10px] {preview.plan.errors.length ? 'text-down' : 'text-up'}">{preview.plan.errors.length ? `${preview.plan.errors.length} issues` : 'Ready'}</span></div><div class="mt-3 grid grid-cols-3 gap-2 text-center text-xs"><div><p class="font-semibold text-up">{preview.plan.create}</p><p class="text-stone-600">Create</p></div><div><p class="font-semibold text-warn">{preview.plan.skip}</p><p class="text-stone-600">Skip</p></div><div><p class="font-semibold text-down">{preview.plan.errors.length}</p><p class="text-stone-600">Issues</p></div></div>{#if preview.plan.errors.length}<div class="mt-3 max-h-36 space-y-1 overflow-auto text-[11px] text-down">{#each preview.plan.errors as issue}<p>Row {issue.row || '—'}{issue.field ? ` · ${issue.field}` : ''}: {issue.message}</p>{/each}</div>{:else}<button type="button" class="mt-4 w-full rounded-md bg-amber-500 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40" disabled={applying} on:click={apply}>{applying ? 'Importing…' : `Import ${preview.plan.create} drafts`}</button>{/if}</div>{/if}
			</aside>
		</div>
	</div>
</div>
