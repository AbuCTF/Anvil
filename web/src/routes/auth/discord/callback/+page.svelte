<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let error = '';
	let registerUrl = '';

	onMount(async () => {
		const params = new URLSearchParams(window.location.search);
		const code = params.get('code');
		const state = params.get('state');
		if (params.get('error') || !code || !state) {
			error = params.get('error_description') || 'Discord sign-in was cancelled.';
			return;
		}
		try {
			const r = await api.discordCallback(code, state);
			localStorage.setItem('accessToken', r.access_token);
			if (r.refresh_token) localStorage.setItem('refreshToken', r.refresh_token);
			auth.login(r.access_token, r.user, r.refresh_token);
			goto('/challenges');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Sign-in failed.';
			// not_registered / verify_email carry a register_url to send them to
			const u = (e as { register_url?: string })?.register_url;
			if (typeof u === 'string') registerUrl = u;
		}
	});
</script>

<svelte:head><title>Signing in · Anvil</title></svelte:head>

<div class="min-h-[60vh] flex items-center justify-center px-4">
	{#if error}
		<div class="text-center">
			<p class="text-sm text-danger mb-4">{error}</p>
			{#if registerUrl}
				<a href={registerUrl} class="inline-block rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 transition-colors hover:bg-stone-50">Register</a>
			{:else}
				<a href="/login" class="inline-block rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 transition-colors hover:bg-stone-50">Back to sign in</a>
			{/if}
		</div>
	{:else}
		<div class="flex items-center gap-2 text-sm text-stone-500">
			<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Signing you in…
		</div>
	{/if}
</div>
