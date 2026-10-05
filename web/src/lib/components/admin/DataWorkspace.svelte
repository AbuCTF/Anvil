<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type DataImportHistory, type DataImportPreview } from '$api';
	import HelpTip from '$lib/components/HelpTip.svelte';

	export let initialEntity = 'categories';
	export let economyEnabled = false;

	const entities = [
		{ id: 'settings', label: 'Event settings', importable: false, selected: true },
		{ id: 'categories', label: 'Categories', importable: true, selected: true },
		{ id: 'challenges', label: 'Challenges', importable: true, selected: true },
		{ id: 'users', label: 'Users', importable: true, selected: true },
		{ id: 'teams', label: 'Teams', importable: true, selected: true },
		{ id: 'team_members', label: 'Team membership', importable: true, selected: true },
		{ id: 'scoreboard', label: 'Scoreboard', importable: false, selected: true },
		{ id: 'solves', label: 'Solve log', importable: false, selected: true },
		{ id: 'submissions', label: 'Submission audit', importable: false, selected: false },
		{ id: 'ledger_balances', label: 'Ledger balances', importable: false, selected: true },
		{ id: 'ledger_history', label: 'Ledger history', importable: false, selected: false }
	];

	let summary: Awaited<ReturnType<typeof api.getDataSummary>> | null = null;
	let history: DataImportHistory[] = [];
	$: availableEntities = entities.filter((entity) => economyEnabled || !entity.id.startsWith('ledger_'));
	let selected = new Set(entities.filter((entity) => entity.selected && (economyEnabled || !entity.id.startsWith('ledger_'))).map((entity) => entity.id));
	let exportFormat: 'bundle' | 'json' | 'csv' = 'bundle';
	let anonymize = false;
	let exportBusy = false;
	let importEntity = initialEntity;
	let importMode: 'create' | 'merge' = 'create';
	let userProvisioning: 'activation_email' | 'generated_credentials' | 'sso_only' = 'activation_email';
	let templateFormat: 'csv' | 'xlsx' | 'json' = 'xlsx';
	let importFile: File | null = null;
	let workbookContent = '';
	let workbookSheets: Array<{ name: string; rows: number; recognized_headers: number; missing_required_headers: string[]; headers: Array<{ source: string; normalized: string; suggested_field: string }> }> = [];
	let workbookSheet = '';
	let workbookFields: string[] = [];
	let workbookRequiredFields: string[] = [];
	let workbookColumnMap: Record<string, string> = {};
	let workbookBusy = false;
	let preview: DataImportPreview | null = null;
	let importBusy = false;
	let confirmation = '';
	let result = '';
	let error = '';

	const input = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-500';
	const label = 'mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-stone-600';
	$: mappedWorkbookFields = new Set(Object.values(workbookColumnMap).filter(Boolean));
	$: missingWorkbookFields = workbookRequiredFields.filter((field) => !mappedWorkbookFields.has(field) && !(importEntity === 'users' && userProvisioning !== 'sso_only' && field === 'username' && mappedWorkbookFields.has('email')));

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

	function selectDefaults() {
		selected = new Set(availableEntities.filter((entity) => entity.selected).map((entity) => entity.id));
		if (exportFormat === 'csv') exportFormat = 'bundle';
	}

	function selectAll() {
		selected = new Set(availableEntities.map((entity) => entity.id));
		if (exportFormat === 'csv') exportFormat = 'bundle';
	}

	function clearSelection() {
		selected = new Set();
		if (exportFormat === 'csv') exportFormat = 'bundle';
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
			await saveDownload(`/admin/data/templates/${importEntity}?format=${templateFormat}`);
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
			const filename = importFile.name.toLowerCase();
			const format = filename.endsWith('.xlsx') ? 'xlsx' : filename.endsWith('.json') ? 'json' : 'csv';
			preview = await api.previewDataImport({ entity: importEntity, format, mode: importMode, source_name: importFile.name, content: format === 'xlsx' ? workbookContent : await importFile.text(), sheet: format === 'xlsx' ? workbookSheet : undefined, column_map: workbookColumnMap, provisioning: importEntity === 'users' ? userProvisioning : undefined });
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Import preview failed';
		} finally {
			importBusy = false;
		}
	}

	function fileAsBase64(file: File) {
		return new Promise<string>((resolve, reject) => {
			const reader = new FileReader();
			reader.onerror = () => reject(new Error('The workbook could not be read'));
			reader.onload = () => resolve(String(reader.result).split(',', 2)[1] ?? '');
			reader.readAsDataURL(file);
		});
	}

	function selectWorkbookSheet() {
		const sheet = workbookSheets.find((candidate) => candidate.name === workbookSheet);
		workbookColumnMap = Object.fromEntries((sheet?.headers ?? []).filter((header) => header.normalized).map((header) => [header.normalized, header.suggested_field]));
		preview = null;
	}

	async function selectImportFile(event: Event) {
		importFile = (event.currentTarget as HTMLInputElement).files?.[0] ?? null;
		preview = null;
		error = '';
		result = '';
		workbookContent = '';
		workbookSheets = [];
		workbookSheet = '';
		workbookFields = [];
		workbookRequiredFields = [];
		workbookColumnMap = {};
		if (!importFile) return;
		if (importFile.size > 5 * 1024 * 1024) {
			error = 'Import files must be 5 MB or smaller.';
			importFile = null;
			return;
		}
		const filename = importFile.name.toLowerCase();
		if (filename.endsWith('.xls')) {
			error = 'Legacy .xls files are not supported. Save the workbook as .xlsx and upload it again.';
			importFile = null;
			return;
		}
		workbookBusy = true;
		try {
			let inspection;
			if (filename.endsWith('.xlsx')) {
				workbookContent = await fileAsBase64(importFile);
				inspection = await api.inspectDataWorkbook({ entity: importEntity, content: workbookContent });
			} else if (filename.endsWith('.csv') || filename.endsWith('.json')) {
				const format = filename.endsWith('.json') ? 'json' : 'csv';
				inspection = await api.inspectDataImport({ entity: importEntity, format, content: await importFile.text() });
			} else {
				throw new Error('Use an .xlsx, .csv or .json file.');
			}
			workbookSheets = inspection.sheets;
			workbookFields = inspection.fields;
			workbookRequiredFields = inspection.required_fields;
			workbookSheet = inspection.recommended_sheet || inspection.sheets[0]?.name || '';
			selectWorkbookSheet();
		} catch (e) {
			error = e instanceof Error ? e.message : 'The file could not be inspected';
			importFile = null;
			workbookContent = '';
		} finally {
			workbookBusy = false;
		}
	}

	async function applyImport() {
		if (!preview || confirmation !== `APPLY ${preview.entity}` || preview.plan.errors.length) return;
		importBusy = true;
		error = '';
		try {
			const applied = await api.applyDataImport(preview.job_id, preview.checksum);
			result = `Applied ${applied.created} create, ${applied.updated} update and ${applied.skipped} skip operations.${applied.activation_emails_queued ? ` ${applied.activation_emails_queued} activation emails queued.` : ''}${applied.credential_emails_queued ? ` ${applied.credential_emails_queued} credential emails queued.` : ''}`;
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

	function downloadIssueReport() {
		if (!preview?.plan.errors.length) return;
		const escape = (value: string | number) => `"${String(value).replaceAll('"', '""')}"`;
		const content = ['row,field,problem', ...preview.plan.errors.map((issue) => [issue.row || '', issue.field || '', issue.message].map(escape).join(','))].join('\n');
		const href = URL.createObjectURL(new Blob([`${content}\n`], { type: 'text/csv;charset=utf-8' }));
		const anchor = document.createElement('a');
		anchor.href = href;
		anchor.download = `${preview.entity}-import-issues.csv`;
		anchor.click();
		URL.revokeObjectURL(href);
	}

	onMount(load);
</script>

<div class="space-y-6">
	{#if summary}
		<div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
			{#each [{ label: 'Users', value: summary.users }, { label: 'Teams', value: summary.teams }, { label: 'Categories', value: summary.categories }, { label: 'Challenges', value: summary.challenges }, { label: 'Solves', value: summary.solves }, { label: 'Open previews', value: summary.pending_imports }] as item}
				<div class="rounded-lg border border-stone-800 bg-stone-900/25 p-4"><p class="text-[10px] uppercase tracking-wider text-stone-600">{item.label}</p><p class="mt-2 text-xl font-semibold tabular-nums text-stone-200">{item.value}</p></div>
			{/each}
		</div>
	{/if}

	{#if error}<div class="rounded-md border border-down/20 bg-down/5 px-4 py-3 text-sm text-down">{error}</div>{/if}
	{#if result}<div class="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-4 py-3 text-sm text-emerald-400">{result}</div>{/if}

	<div class="grid gap-6 xl:grid-cols-2">
		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="flex items-start justify-between gap-3 border-b border-stone-800 px-5 py-4"><div><h2 class="flex items-center gap-1.5 text-sm font-semibold text-stone-100">Export <HelpTip text="The Anvil bundle contains JSON and CSV plus checksums. Full Ledger history and submission audit are optional because mature events can make them large; choose one item and CSV for a focused spreadsheet." /></h2><p class="mt-1 text-xs text-stone-500">Portable configuration, results, and audit data.</p></div><div class="flex items-center gap-2 text-[10px]"><button type="button" on:click={selectDefaults} class="text-stone-500 hover:text-stone-200">Recommended</button><button type="button" on:click={selectAll} class="text-stone-500 hover:text-stone-200">All</button><button type="button" on:click={clearSelection} class="text-stone-500 hover:text-stone-200">Clear</button></div></div>
			<div class="space-y-5 p-5">
				<div class="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-2 2xl:grid-cols-3">
					{#each availableEntities as entity}
						<label class="flex cursor-pointer items-center gap-2 rounded-md border border-stone-800 px-3 py-2 text-xs text-stone-400"><input type="checkbox" checked={selected.has(entity.id)} on:change={() => toggleEntity(entity.id)} class="accent-amber-500" />{entity.label}</label>
					{/each}
				</div>
				<div class="grid gap-4 sm:grid-cols-2">
					<label><span class={label}>Format</span><select class={input} bind:value={exportFormat}><option value="bundle">Anvil bundle (.zip)</option><option value="json">Normalized JSON</option><option value="csv" disabled={selected.size !== 1}>CSV (one entity)</option></select></label>
					<label class="flex items-end"><span class="flex w-full items-center justify-between rounded-md border border-stone-800 px-3 py-2.5 text-sm text-stone-400">Anonymize identities<input type="checkbox" bind:checked={anonymize} class="h-4 w-4 accent-amber-500" /></span></label>
				</div>
				<div class="flex items-start gap-2 rounded-md border border-stone-800 bg-stone-950/50 p-3 text-xs leading-relaxed text-stone-500"><Icon icon="mdi:shield-lock-outline" class="mt-0.5 h-4 w-4 shrink-0 text-stone-600" /><span>Secrets and live runtime credentials are excluded. Submission exports contain outcomes and metadata, never submitted flag values. Bundles include a checksum manifest.</span></div>
				<button type="button" on:click={runExport} disabled={exportBusy || !selected.size || (exportFormat === 'csv' && selected.size !== 1)} class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40"><Icon icon={exportBusy ? 'mdi:loading' : 'mdi:download'} class="h-4 w-4 {exportBusy ? 'animate-spin' : ''}" />Download export</button>
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="flex items-center gap-1.5 text-sm font-semibold text-stone-100">Import <HelpTip align="right" text="Every import is parsed and validated first. Apply runs once in a database transaction: any row failure rolls back the whole import. If data changes after preview, Anvil asks for a fresh preview." /></h2><p class="mt-1 text-xs text-stone-500">Dry-run first, then apply one atomic plan.</p></div>
			<div class="space-y-4 p-5">
				<div class="grid gap-4 sm:grid-cols-2">
					<label><span class={label}>Entity</span><select class={input} bind:value={importEntity} on:change={() => { preview = null; importFile = null; workbookContent = ''; workbookSheets = []; workbookSheet = ''; workbookFields = []; workbookRequiredFields = []; workbookColumnMap = {}; }}>{#each availableEntities.filter((entity) => entity.importable) as entity}<option value={entity.id}>{entity.label}</option>{/each}</select></label>
					<label><span class={label}>Mode</span><select class={input} bind:value={importMode}><option value="create">Create only</option><option value="merge">Create and update</option></select></label>
				</div>
				{#if importEntity === 'users'}
					<label><span class={label}>Account access</span><select class={input} bind:value={userProvisioning}><option value="activation_email">Email secure activation links</option><option value="generated_credentials">Email temporary credentials</option><option value="sso_only">Provision for SSO only</option></select><span class="mt-1.5 block text-[11px] leading-relaxed text-stone-600">{userProvisioning === 'activation_email' ? 'Each recipient sets their own password from a single-use, 48-hour link.' : userProvisioning === 'generated_credentials' ? 'Missing usernames are generated. Each new participant receives a unique temporary password and must replace it at first sign-in.' : 'Accounts have no local password and enter through the configured identity provider.'}</span></label>
				{/if}
				<div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]"><label class="min-w-0"><span class={label}>Excel, CSV or JSON file</span><input type="file" accept=".xlsx,.csv,.json,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/csv,application/json" on:change={selectImportFile} class="block w-full text-xs text-stone-500 file:mr-3 file:rounded-md file:border-0 file:bg-stone-800 file:px-3 file:py-2 file:text-xs file:text-stone-300" /></label><div><span class={label}>Blank template</span><div class="flex"><select class="rounded-l-md border border-r-0 border-stone-800 bg-stone-950 px-2.5 py-2 text-xs text-stone-300 outline-none" bind:value={templateFormat}><option value="xlsx">Excel</option><option value="csv">CSV</option><option value="json">JSON</option></select><button type="button" on:click={downloadTemplate} class="inline-flex items-center gap-1 rounded-r-md border border-stone-800 px-2.5 py-2 text-xs text-stone-300 hover:bg-stone-800/40"><Icon icon="mdi:download" class="h-3.5 w-3.5" />Download</button></div></div></div>
				{#if workbookBusy}<div class="flex items-center gap-2 rounded-md border border-stone-800 bg-stone-950/50 px-3 py-2.5 text-xs text-stone-500"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />Inspecting columns…</div>{/if}
				{#if workbookSheets.length}
					{#if importFile?.name.toLowerCase().endsWith('.xlsx')}<label><span class={label}>Workbook sheet</span><select class={input} bind:value={workbookSheet} on:change={selectWorkbookSheet}>{#each workbookSheets as sheet}<option value={sheet.name}>{sheet.name} · {sheet.rows > 5000 ? '5,000+ rows' : `${sheet.rows} rows`} · {sheet.recognized_headers} matched</option>{/each}</select><span class="mt-1.5 block text-[11px] text-stone-600">The closest matching sheet was selected automatically. Choose another if needed.</span></label>{/if}
					<details class="rounded-md border border-stone-800 bg-stone-950/40" open={missingWorkbookFields.length > 0}><summary class="flex cursor-pointer items-center justify-between gap-3 px-3 py-2.5 text-xs font-medium text-stone-300"><span>Match source columns</span><span class="text-[10px] {missingWorkbookFields.length ? 'text-warn' : 'text-up'}">{missingWorkbookFields.length ? `${missingWorkbookFields.length} required field${missingWorkbookFields.length === 1 ? '' : 's'} missing` : 'Ready'}</span></summary><div class="grid gap-2 border-t border-stone-800 p-3 sm:grid-cols-2">{#each workbookSheets.find((sheet) => sheet.name === workbookSheet)?.headers ?? [] as header}<label class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] items-center gap-2"><span class="truncate text-xs text-stone-500" title={header.source}>{header.source || '(empty column)'}</span><select class="rounded-md border border-stone-800 bg-stone-950 px-2 py-2 text-xs text-stone-300 outline-none focus:border-stone-600" bind:value={workbookColumnMap[header.normalized]} on:change={() => preview = null}><option value="">Ignore</option>{#each workbookFields as field}<option value={field}>{field.replaceAll('_', ' ')}</option>{/each}</select></label>{/each}</div>{#if missingWorkbookFields.length}<p class="border-t border-stone-800 px-3 py-2 text-[11px] text-warn">Map: {missingWorkbookFields.join(', ').replaceAll('_', ' ')}</p>{:else}<p class="border-t border-stone-800 px-3 py-2 text-[11px] text-stone-600">Extra columns may be ignored. The uploaded file is never modified.</p>{/if}</details>
				{/if}
				<div class="flex flex-wrap gap-2 text-[10px] text-stone-500"><span class="rounded-full border border-stone-800 px-2 py-1">Up to 5 MB</span><span class="rounded-full border border-stone-800 px-2 py-1">Up to 5,000 rows</span><span class="rounded-full border border-stone-800 px-2 py-1">Excel .xlsx · CSV · JSON · Anvil export</span></div>
				{#if importEntity === 'challenges'}
					<details class="rounded-md border border-stone-800 bg-stone-950/40 px-3 py-2.5 text-xs text-stone-500"><summary class="cursor-pointer font-medium text-stone-300">Challenge repositories</summary><div class="mt-2 space-y-1.5 leading-relaxed"><p>Import categories first, then one challenge per row. Registry images, points, delivery, judging, scoring, author and release state are portable.</p><p>Add handouts, flags, hints, grader secrets and VM images after import.</p></div></details>
				{/if}
				<button type="button" on:click={previewImport} disabled={!importFile || importBusy || workbookBusy || !workbookSheet || missingWorkbookFields.length > 0} class="inline-flex w-full items-center justify-center gap-2 rounded-md border border-stone-700 px-4 py-2.5 text-sm font-medium text-stone-200 disabled:opacity-40"><Icon icon={importBusy ? 'mdi:loading' : 'mdi:magnify-scan'} class="h-4 w-4 {importBusy ? 'animate-spin' : ''}" />Dry-run import</button>
			</div>
		</section>
	</div>

	{#if preview}
		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="flex flex-col gap-3 border-b border-stone-800 px-5 py-4 sm:flex-row sm:items-center sm:justify-between"><div><h2 class="text-sm font-semibold text-stone-100">Import plan</h2><p class="mt-1 break-all text-xs text-stone-600">{preview.source_name} · {preview.row_count} rows · expires {new Date(preview.expires_at).toLocaleString()}</p></div><span class="rounded-full px-2.5 py-1 text-[10px] {preview.plan.errors.length ? 'bg-down/10 text-down' : 'bg-emerald-500/10 text-emerald-400'}">{preview.plan.errors.length ? `${preview.plan.errors.length} issues` : 'Ready to apply'}</span></div>
			<div class="p-5">
				<div class="grid grid-cols-3 gap-3"><div class="rounded-md bg-emerald-500/5 p-3 text-center"><p class="text-xl font-semibold text-emerald-400">{preview.plan.create}</p><p class="text-[10px] uppercase text-stone-600">Create</p></div><div class="rounded-md bg-amber-500/5 p-3 text-center"><p class="text-xl font-semibold text-amber-400">{preview.plan.update}</p><p class="text-[10px] uppercase text-stone-600">Update</p></div><div class="rounded-md bg-stone-950 p-3 text-center"><p class="text-xl font-semibold text-stone-400">{preview.plan.skip}</p><p class="text-[10px] uppercase text-stone-600">Skip</p></div></div>
				{#if preview.plan.errors.length}
					<div class="mt-4 flex items-center justify-between gap-3"><p class="text-xs text-stone-500">Fix the listed rows in the source file, then run the preview again. Nothing has been written.</p><button type="button" on:click={downloadIssueReport} class="shrink-0 text-xs text-stone-300 hover:text-stone-100">Download issues</button></div>
					<div class="mt-3 max-h-64 overflow-auto rounded-md border border-down/20"><table class="w-full text-left text-xs"><thead class="sticky top-0 bg-stone-950 text-stone-500"><tr><th class="px-3 py-2">Row</th><th class="px-3 py-2">Field</th><th class="px-3 py-2">Problem</th></tr></thead><tbody class="divide-y divide-stone-800">{#each preview.plan.errors as issue}<tr><td class="px-3 py-2 tabular-nums text-stone-500">{issue.row || '—'}</td><td class="px-3 py-2 text-stone-400">{issue.field || '—'}</td><td class="px-3 py-2 text-down">{issue.message}</td></tr>{/each}</tbody></table></div>
				{:else}
					{#if preview.entity === 'users' && preview.provisioning === 'generated_credentials' && preview.plan.create > 0}
						<div class="mt-4 rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="text-xs font-medium text-stone-300">{preview.plan.create} credential {preview.plan.create === 1 ? 'email' : 'emails'} will be queued</p><p class="mt-1 text-[11px] leading-relaxed text-stone-600">Temporary passwords are encrypted in the mail queue, never returned in this screen or stored as plaintext. New usernames include {preview.plan.rows.filter((row) => row.action === 'create').slice(0, 5).map((row) => row.key).join(', ')}{preview.plan.create > 5 ? '…' : ''}.</p></div>
					{/if}
					<div class="mt-4 grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]"><label><span class={label}>Type APPLY {preview.entity}</span><input class={input} bind:value={confirmation} autocomplete="off" /></label><button type="button" on:click={applyImport} disabled={importBusy || confirmation !== `APPLY ${preview.entity}`} class="self-end rounded-md bg-amber-500 px-5 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-40">Apply once</button></div>
				{/if}
			</div>
		</section>
	{/if}

	<section class="rounded-lg border border-stone-800 bg-stone-900/25">
		<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Import activity</h2><p class="mt-1 text-xs text-stone-500">A preview is only a reviewable plan. It expires after 24 hours unless applied.</p></div>
		{#if history.length}
			<div class="divide-y divide-stone-800/70">{#each history as item}<div class="flex flex-col gap-2 px-5 py-3 text-xs sm:flex-row sm:items-center sm:justify-between"><div class="min-w-0"><span class="font-medium text-stone-300">{item.source_name}</span><span class="ml-2 text-stone-600">{item.entity} · {item.mode} · {item.row_count} rows</span>{#if item.error}<p class="mt-1 truncate text-down" title={item.error}>{item.error}</p>{/if}</div><span class="w-fit rounded-full px-2 py-1 text-[10px] {item.status === 'applied' ? 'bg-emerald-500/10 text-emerald-400' : item.status === 'pending' ? 'bg-amber-500/10 text-amber-400' : item.status === 'failed' ? 'bg-down/10 text-down' : 'bg-stone-800 text-stone-500'}">{item.status === 'pending' ? 'Preview ready' : item.status}</span></div>{/each}</div>
		{:else}<div class="px-5 py-10 text-center text-sm text-stone-600">No import previews yet.</div>{/if}
	</section>
</div>
