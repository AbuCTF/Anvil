<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type InviteCodeSummary } from '$api';
	import { confirmDialog } from '$lib/stores/dialog';

	let codes: InviteCodeSummary[] = [];
	let maxUses = 1;
	let expires = '';
	let createdCode = '';
	let busy = false;
	let error = '';

	async function load() {
		try {
			codes = (await api.getInviteCodes()).codes;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load invite codes';
		}
	}

	async function create() {
		busy = true;
		error = '';
		createdCode = '';
		try {
			const result = await api.createInviteCode(maxUses, expires ? new Date(expires).toISOString() : undefined);
			createdCode = result.code;
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not create invite code';
		} finally {
			busy = false;
		}
	}

	async function remove(code: InviteCodeSummary) {
		if (!(await confirmDialog({ title: 'Revoke invite', message: `Revoke the invite ending ${code.code_suffix}?`, confirmLabel: 'Revoke', danger: true }))) return;
		busy = true;
		error = '';
		try {
			await api.deleteInviteCode(code.id);
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not revoke invite code';
		} finally {
			busy = false;
		}
	}

	async function copyCreated() {
		await navigator.clipboard.writeText(createdCode);
	}

	onMount(load);
</script>

<div class="rounded-md border border-stone-800 bg-stone-950/50 p-4 md:col-span-2">
	<div class="flex items-start justify-between gap-4">
		<div><h3 class="text-sm font-medium text-stone-200">Invite codes</h3><p class="mt-1 text-xs text-stone-500">Codes are shown in full only once. Existing codes display only their suffix.</p></div>
		<span class="rounded-full bg-stone-900 px-2 py-1 text-[10px] text-stone-500">{codes.length} active</span>
	</div>
	<div class="mt-4 grid gap-3 sm:grid-cols-[110px_minmax(0,1fr)_auto]">
		<label><span class="mb-1.5 block text-[10px] uppercase tracking-wider text-stone-600">Maximum uses</span><input type="number" min="1" max="10000" bind:value={maxUses} class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-200" /></label>
		<label><span class="mb-1.5 block text-[10px] uppercase tracking-wider text-stone-600">Expires</span><input type="datetime-local" bind:value={expires} class="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-200" /></label>
		<button type="button" on:click={create} disabled={busy || maxUses < 1 || maxUses > 10000} class="self-end rounded-md bg-stone-100 px-4 py-2 text-sm font-medium text-stone-950 disabled:opacity-40">Create</button>
	</div>
	{#if createdCode}
		<div class="mt-3 flex flex-col gap-2 rounded-md border border-emerald-500/20 bg-emerald-500/5 p-3 sm:flex-row sm:items-center sm:justify-between">
			<code class="break-all text-xs text-emerald-300">{createdCode}</code>
			<button type="button" on:click={copyCreated} class="shrink-0 text-xs text-emerald-400 hover:text-emerald-300">Copy code</button>
		</div>
	{/if}
	{#if error}<p class="mt-3 text-xs text-down">{error}</p>{/if}
	{#if codes.length}
		<div class="mt-4 divide-y divide-stone-800/70 border-t border-stone-800">
			{#each codes as code}
				<div class="flex items-center justify-between gap-3 py-3 text-xs">
					<div><span class="font-medium text-stone-300">•••• {code.code_suffix}</span><span class="ml-3 text-stone-600">{code.current_uses}/{code.max_uses} used{code.expires_at ? ` · expires ${new Date(code.expires_at).toLocaleString()}` : ''}</span></div>
					<button type="button" aria-label="Revoke invite" on:click={() => remove(code)} disabled={busy} class="text-stone-600 hover:text-down"><Icon icon="mdi:delete-outline" class="h-4 w-4" /></button>
				</div>
			{/each}
		</div>
	{/if}
</div>
