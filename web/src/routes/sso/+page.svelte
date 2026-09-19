<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let error = '';

	onMount(async () => {
		// token arrives in the fragment so it never hits history, referrers, or server logs
		const hash = window.location.hash.replace(/^#/, '');
		const token = new URLSearchParams(hash).get('token');
		history.replaceState(null, '', window.location.pathname);
		if (!token) {
			error = 'Missing sign-in token.';
			return;
		}
		try {
			const r = await api.ssoLogin(token);
			localStorage.setItem('accessToken', r.access_token);
			if (r.refresh_token) localStorage.setItem('refreshToken', r.refresh_token);
			auth.login(r.access_token, r.user, r.refresh_token);
			goto('/challenges');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Sign-in failed.';
		}
	});
</script>

<svelte:head><title>Signing in · Anvil</title></svelte:head>

<div class="min-h-[60vh] flex items-center justify-center px-4">
	{#if error}
		<div class="text-center">
			<p class="text-sm text-down mb-4">{error}</p>
			<a href="/login" class="inline-block rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 transition-colors hover:bg-stone-50">Back to sign in</a>
		</div>
	{:else}
		<div class="flex items-center gap-2 text-sm text-stone-500">
			<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Signing you in…
		</div>
	{/if}
</div>
