<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type DataImportHistory, type DataImportPreview } from '$api';

	const entities = [
		{ id: 'settings', label: 'Event settings', importable: false },
		{ id: 'categories', label: 'Categories', importable: true },
		{ id: 'challenges', label: 'Challenges', importable: true },
		{ id: 'users', label: 'Users', importable: true },
		{ id: 'teams', label: 'Teams', importable: true },
		{ id: 'team_members', label: 'Team membership', importable: true }
	];

	let summary: Awaited<ReturnType<typeof api.getDataSummary>> | null = null;
	let history: DataImportHistory[] = [];
	let selected = new Set(entities.map((entity) => entity.id));
	let exportFormat: 'bundle' | 'json' | 'csv' = 'bundle';
	let anonymize = false;
	let exportBusy = false;
	let importEntity = 'categories';
	let importMode: 'create' | 'merge' = 'create';
	let importFile: File | null = null;
	let preview: DataImportPreview | null = null;
	let importBusy = false;
	let confirmation = '';
	let result = '';
	let error = '';

	const input = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-500';
	const label = 'mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-stone-600';

	async function load() {
		try {
			[summary, history] = await Promise.all([api.getDataSummary(), api.getDataImports().then((response) => response.imports)]);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Data controls are unavailable';
		}
	}

	function toggleEntity(entity: string) {
		const next = new Set(selected);
		if (next.has(entity)) next.delete(entity);
		else next.add(entity);
		selected = next;
		if (exportFormat === 'csv' && next.size !== 1) exportFormat = 'bundle';
	}

	async function saveDownload(path: string) {
		const { blob, filename } = await api.downloadAdminData(path);
		const url = URL.createObjectURL(blob);
		const anchor = document.createElement('a');
		anchor.href = url;
		anchor.download = filename;
		anchor.click();
		URL.revokeObjectURL(url);
	}

	async function runExport() {
		if (!selected.size) return;
		exportBusy = true;
		error = '';
		try {
			const query = new URLSearchParams({ format: exportFormat, entities: [...selected].join(','), anonymize: String(anonymize) });
			await saveDownload(`/admin/data/export?${query}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Export failed';
		} finally {
			exportBusy = false;
		}
	}

	async function downloadTemplate() {
		try {
			await saveDownload(`/admin/data/templates/${importEntity}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Template download failed';
		}
	}

	async function previewImport() {
		if (!importFile) return;
		importBusy = true;
		error = '';
		result = '';
		confirmation = '';
		try {
			const extension = importFile.name.toLowerCase().endsWith('.json') ? 'json' : 'csv';
			preview = await api.previewDataImport({ entity: importEntity, format: extension, mode: importMode, source_name: importFile.name, content: await importFile.text() });
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Import preview failed';
		} finally {
			importBusy = false;
		}
	}

	async function applyImport() {
		if (!preview || confirmation !== `APPLY ${preview.entity}` || preview.plan.errors.length) return;
		importBusy = true;
		error = '';
		try {
			const applied = await api.applyDataImport(preview.job_id, preview.checksum);
			result = `Applied ${applied.created} create, ${applied.updated} update and ${applied.skipped} skip operations.`;
			preview = null;
			importFile = null;
			confirmation = '';
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Import failed';
		} finally {
			importBusy = false;
		}
	}

	onMount(load);
</script>

<div class="space-y-6">
	{#if summary}
		<div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
			{#each [{ label: 'Users', value: summary.users }, { label: 'Teams', value: summary.teams }, { label: 'Categories', value: summary.categories }, { label: 'Challenges', value: summary.challenges }, { label: 'Solves', value: summary.solves }, { label: 'Pending', value: summary.pending_imports }] as item}
				<div class="rounded-lg border border-stone-800 bg-stone-900/25 p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">{item.label}</p><p class="mt-2 text-xl font-semibold tabular-nums text-stone-200">{item.value}</p></div>
			{/each}
		</div>
	{/if}

	{#if error}<div class="rounded-md border border-down/20 bg-down/5 px-4 py-3 text-sm text-down">{error}</div>{/if}
	{#if result}<div class="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-4 py-3 text-sm text-emerald-400">{result}</div>{/if}

	<div class="grid gap-6 xl:grid-cols-2">
		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Portable export</h2><p class="mt-1 text-xs text-stone-500">Create a versioned bundle, JSON snapshot or spreadsheet export.</p></div>
			<div class="space-y-5 p-5">
				<div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
					{#each entities as entity}
						<label class="flex cursor-pointer items-center gap-2 rounded-md border border-stone-800 px-3 py-2 text-xs text-stone-400"><input type="checkbox" checked={selected.has(entity.id)} on:change={() => toggleEntity(entity.id)} class="accent-amber-500" />{entity.label}</label>
					{/each}
				</div>
				<div class="grid gap-4 sm:grid-cols-2">
					<label><span class={label}>Format</span><select class={input} bind:value={exportFormat}><option value="bundle">Anvil bundle (.zip)</option><option value="json">Normalized JSON</option><option value="csv" disabled={selected.size !== 1}>CSV (one entity)</option></select></label>
					<label class="flex items-end"><span class="flex w-full items-center justify-between rounded-md border border-stone-800 px-3 py-2.5 text-sm text-stone-400">Anonymize identities<input type="checkbox" bind:checked={anonymize} class="h-4 w-4 accent-amber-500" /></span></label>
				</div>
				<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3 text-xs leading-relaxed text-stone-500">Secrets, passwords, sessions, flags, grader keys, team join codes, runtime credentials and active instances are excluded. The bundle manifest includes SHA-256 checksums for every data file.</div>
				<button type="button" on:click={runExport} disabled={exportBusy || !selected.size || (exportFormat === 'csv' && selected.size !== 1)} class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40"><Icon icon={exportBusy ? 'mdi:loading' : 'mdi:download'} class="h-4 w-4 {exportBusy ? 'animate-spin' : ''}" />Download export</button>
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Validated import</h2><p class="mt-1 text-xs text-stone-500">Preview every operation before it can touch event data.</p></div>
			<div class="space-y-4 p-5">
				<div class="grid gap-4 sm:grid-cols-2">
					<label><span class={label}>Entity</span><select class={input} bind:value={importEntity} on:change={() => { preview = null; importFile = null; }}>{#each entities.filter((entity) => entity.importable) as entity}<option value={entity.id}>{entity.label}</option>{/each}</select></label>
					<label><span class={label}>Mode</span><select class={input} bind:value={importMode}><option value="create">Create only</option><option value="merge">Create and update</option></select></label>
				</div>
				<div class="flex items-center justify-between gap-3"><label class="min-w-0 flex-1"><span class={label}>CSV or JSON file</span><input type="file" accept=".csv,.json,text/csv,application/json" on:change={(event) => { importFile = (event.currentTarget as HTMLInputElement).files?.[0] ?? null; preview = null; }} class="block w-full text-xs text-stone-500 file:mr-3 file:rounded-md file:border-0 file:bg-stone-800 file:px-3 file:py-2 file:text-xs file:text-stone-300" /></label><button type="button" on:click={downloadTemplate} class="mt-5 shrink-0 text-xs text-stone-400 hover:text-stone-200">CSV template</button></div>
				<button type="button" on:click={previewImport} disabled={!importFile || importBusy} class="inline-flex w-full items-center justify-center gap-2 rounded-md border border-stone-700 px-4 py-2.5 text-sm font-medium text-stone-200 disabled:opacity-40"><Icon icon={importBusy ? 'mdi:loading' : 'mdi:magnify-scan'} class="h-4 w-4 {importBusy ? 'animate-spin' : ''}" />Dry-run import</button>
			</div>
		</section>
	</div>

	{#if preview}
		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="flex flex-col gap-3 border-b border-stone-800 px-5 py-4 sm:flex-row sm:items-center sm:justify-between"><div><h2 class="text-sm font-semibold text-stone-100">Import plan</h2><p class="mt-1 break-all text-xs text-stone-600">{preview.source_name} · {preview.checksum}</p></div><span class="rounded-full px-2.5 py-1 text-[10px] {preview.plan.errors.length ? 'bg-down/10 text-down' : 'bg-emerald-500/10 text-emerald-400'}">{preview.plan.errors.length ? `${preview.plan.errors.length} errors` : 'Validated'}</span></div>
			<div class="p-5">
				<div class="grid grid-cols-3 gap-3"><div class="rounded-md bg-emerald-500/5 p-3 text-center"><p class="text-xl font-semibold text-emerald-400">{preview.plan.create}</p><p class="text-[10px] uppercase text-stone-600">Create</p></div><div class="rounded-md bg-amber-500/5 p-3 text-center"><p class="text-xl font-semibold text-amber-400">{preview.plan.update}</p><p class="text-[10px] uppercase text-stone-600">Update</p></div><div class="rounded-md bg-stone-950 p-3 text-center"><p class="text-xl font-semibold text-stone-400">{preview.plan.skip}</p><p class="text-[10px] uppercase text-stone-600">Skip</p></div></div>
				{#if preview.plan.errors.length}
					<div class="mt-4 max-h-64 overflow-auto rounded-md border border-down/20"><table class="w-full text-left text-xs"><thead class="sticky top-0 bg-stone-950 text-stone-500"><tr><th class="px-3 py-2">Row</th><th class="px-3 py-2">Field</th><th class="px-3 py-2">Problem</th></tr></thead><tbody class="divide-y divide-stone-800">{#each preview.plan.errors as issue}<tr><td class="px-3 py-2 tabular-nums text-stone-500">{issue.row || '—'}</td><td class="px-3 py-2 text-stone-400">{issue.field || '—'}</td><td class="px-3 py-2 text-down">{issue.message}</td></tr>{/each}</tbody></table></div>
				{:else}
					<div class="mt-4 grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]"><label><span class={label}>Type APPLY {preview.entity}</span><input class={input} bind:value={confirmation} autocomplete="off" /></label><button type="button" on:click={applyImport} disabled={importBusy || confirmation !== `APPLY ${preview.entity}`} class="self-end rounded-md bg-amber-500 px-5 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40">Apply once</button></div>
				{/if}
			</div>
		</section>
	{/if}

	<section class="rounded-lg border border-stone-800 bg-stone-900/25">
		<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Recent imports</h2><p class="mt-1 text-xs text-stone-500">Previews expire after 24 hours. Applied payloads are erased after the transaction commits.</p></div>
		{#if history.length}
			<div class="divide-y divide-stone-800/70">{#each history as item}<div class="flex flex-col gap-2 px-5 py-3 text-xs sm:flex-row sm:items-center sm:justify-between"><div><span class="font-medium text-stone-300">{item.source_name}</span><span class="ml-2 text-stone-600">{item.entity} · {item.mode} · {item.row_count} rows</span></div><span class="rounded-full px-2 py-1 text-[10px] {item.status === 'applied' ? 'bg-emerald-500/10 text-emerald-400' : item.status === 'pending' ? 'bg-amber-500/10 text-amber-400' : 'bg-stone-800 text-stone-500'}">{item.status}</span></div>{/each}</div>
		{:else}<div class="px-5 py-10 text-center text-sm text-stone-600">No import previews yet.</div>{/if}
	</section>
</div>
