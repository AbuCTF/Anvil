<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type RegistryCredential } from '$api';
	import { alertDialog, confirmDialog } from '$lib/stores/dialog';

	let credentials: RegistryCredential[] = [];
	let loading = true;
	let editing: 'docker.io' | 'ghcr.io' | '' = '';
	let username = '';
	let token = '';
	let saving = false;

	const field = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-xs text-stone-200 outline-none focus:border-stone-600';

	async function load() {
		loading = true;
		try {
			credentials = (await api.getRegistryCredentials()).credentials;
		} catch (e) {
			await alertDialog({ title: 'Registry access', message: e instanceof Error ? e.message : 'Could not load registry access.' });
		} finally {
			loading = false;
		}
	}

	function begin(registry: 'docker.io' | 'ghcr.io') {
		const current = credentials.find((item) => item.registry === registry);
		editing = registry;
		username = current?.username ?? '';
		token = '';
	}

	async function save() {
		if (!editing || !username.trim() || !token) return;
		saving = true;
		try {
			await api.saveRegistryCredential(editing, username.trim(), token);
			editing = '';
			username = '';
			token = '';
			await load();
		} catch (e) {
			await alertDialog({ title: 'Registry access', message: e instanceof Error ? e.message : 'Could not save registry access.' });
		} finally {
			saving = false;
		}
	}

	async function remove(registry: 'docker.io' | 'ghcr.io') {
		if (!(await confirmDialog({ title: 'Remove registry access', message: `Remove the saved credential for ${registry}? New private image pulls will fail until it is reconnected.`, confirmLabel: 'Remove', danger: true }))) return;
		try {
			await api.deleteRegistryCredential(registry);
			await load();
		} catch (e) {
			await alertDialog({ title: 'Registry access', message: e instanceof Error ? e.message : 'Could not remove registry access.' });
		}
	}

	onMount(load);
</script>

<details class="mt-3 rounded-md border border-stone-800 bg-stone-950/40">
	<summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-2.5 text-xs text-stone-400"><span class="flex items-center gap-2"><Icon icon="mdi:key-chain-variant" class="h-4 w-4" />Private registry access</span><span class="text-[10px] text-stone-600">Docker Hub · GHCR</span></summary>
	<div class="space-y-3 border-t border-stone-800 p-3">
		<p class="text-[11px] text-stone-600">Read-only · encrypted at rest</p>
		{#if loading}
			<p class="text-xs text-stone-600">Loading…</p>
		{:else}
			{#each [{ registry: 'docker.io' as const, label: 'Docker Hub' }, { registry: 'ghcr.io' as const, label: 'GitHub Container Registry' }] as provider}
				{@const current = credentials.find((item) => item.registry === provider.registry)}
				<div class="rounded-md border border-stone-800 p-3">
					<div class="flex items-center justify-between gap-3"><div><p class="text-xs font-medium text-stone-300">{provider.label}</p><p class="mt-0.5 text-[11px] text-stone-600">{current ? `Connected as ${current.username}` : 'No credential saved'}</p></div><div class="flex gap-2"><button type="button" class="text-xs text-stone-400 hover:text-stone-200" on:click={() => begin(provider.registry)}>{current ? 'Rotate' : 'Connect'}</button>{#if current}<button type="button" class="text-xs text-down" on:click={() => remove(provider.registry)}>Remove</button>{/if}</div></div>
					{#if editing === provider.registry}
						<div class="mt-3 grid gap-2 sm:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_auto]"><input class={field} bind:value={username} autocomplete="off" placeholder="Registry username" /><input type="password" class={field} bind:value={token} autocomplete="new-password" placeholder="Read-only access token" /><div class="flex gap-2"><button type="button" class="rounded-md bg-stone-100 px-3 py-2 text-xs font-medium text-stone-950 disabled:opacity-40" disabled={saving || !username.trim() || !token} on:click={save}>{saving ? 'Saving…' : 'Save'}</button><button type="button" class="px-2 text-xs text-stone-500" on:click={() => editing = ''}>Cancel</button></div></div>
					{/if}
				</div>
			{/each}
		{/if}
	</div>
</details>
