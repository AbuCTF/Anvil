<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import BrandLogo from '$lib/components/BrandLogo.svelte';
	import { platformInfo, loadPlatformInfo } from '$lib/stores/platform';

	let token = '';
	let password = '';
	let confirmation = '';
	let submitting = false;
	let error = '';

	onMount(() => {
		loadPlatformInfo();
		const params = new URLSearchParams(window.location.hash.slice(1));
		token = params.get('token') ?? '';
		history.replaceState(null, '', '/change-password');
		if (!token) error = 'This password change session is incomplete. Sign in again with your temporary password.';
	});

	async function complete() {
		if (!token) return;
		if (password.length < 8) {
			error = 'Use at least 8 characters.';
			return;
		}
		if (password !== confirmation) {
			error = 'The passwords do not match.';
			return;
		}
		submitting = true;
		error = '';
		try {
			const response = await api.completeInitialPasswordChange(token, password);
			auth.login(response.access_token, response.user, response.refresh_token);
			token = '';
			password = '';
			confirmation = '';
			window.location.href = response.user.role === 'admin' ? '/admin' : '/challenges';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Password change failed.';
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>Choose a password · {$platformInfo?.name ?? 'Anvil'}</title><meta name="referrer" content="no-referrer" /></svelte:head>

<div class="flex min-h-[calc(100vh-4rem)] items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center"><BrandLogo className="mx-auto mb-4 h-9 w-auto max-w-56" /><h1 class="text-2xl font-semibold tracking-tight text-stone-100">Choose your password</h1><p class="mt-1.5 text-sm text-stone-500">Replace the temporary password before continuing.</p></div>
		<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-5 sm:p-6">
			{#if !token}
				<div class="text-center"><Icon icon="mdi:shield-alert-outline" class="mx-auto h-9 w-9 text-down" /><p class="mt-3 text-sm text-down">{error}</p><a href="/login" class="mt-5 inline-flex w-full items-center justify-center rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950">Return to sign in</a></div>
			{:else}
				<form class="space-y-4" on:submit|preventDefault={complete}>
					{#if error}<p class="flex items-start gap-1.5 text-sm text-down"><Icon icon="mdi:alert-circle-outline" class="mt-0.5 h-4 w-4 shrink-0" />{error}</p>{/if}
					<label class="block"><span class="mb-1.5 block text-sm font-medium text-stone-300">New password</span><input type="password" autocomplete="new-password" minlength="8" maxlength="72" bind:value={password} required class="w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-600" /></label>
					<label class="block"><span class="mb-1.5 block text-sm font-medium text-stone-300">Confirm password</span><input type="password" autocomplete="new-password" minlength="8" maxlength="72" bind:value={confirmation} required class="w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-600" /></label>
					<button type="submit" disabled={submitting} class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-50"><Icon icon={submitting ? 'mdi:loading' : 'mdi:shield-check-outline'} class="h-4 w-4 {submitting ? 'animate-spin' : ''}" />{submitting ? 'Securing account…' : 'Set password and continue'}</button>
				</form>
			{/if}
		</div>
	</div>
</div>
