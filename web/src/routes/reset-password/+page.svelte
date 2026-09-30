<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import BrandLogo from '$lib/components/BrandLogo.svelte';

	let token = '';
	let username = '';
	let displayName = '';
	let eventName = 'Anvil';
	let password = '';
	let confirmation = '';
	let loading = true;
	let submitting = false;
	let complete = false;
	let error = '';

	onMount(async () => {
		const params = new URLSearchParams(window.location.hash.slice(1));
		token = params.get('token') ?? '';
		history.replaceState(null, '', '/reset-password');
		if (!token) {
			error = 'This reset link is incomplete.';
			loading = false;
			return;
		}
		try {
			const details = await api.inspectPasswordReset(token);
			username = details.username;
			displayName = details.display_name;
			eventName = details.event_name;
		} catch (e) {
			error = e instanceof Error ? e.message : 'This reset link is invalid or expired.';
		} finally {
			loading = false;
		}
	});

	async function resetPassword() {
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
			await api.completePasswordReset(token, password);
			complete = true;
			token = '';
			password = '';
			confirmation = '';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Password reset failed.';
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>Set new password · {eventName}</title><meta name="referrer" content="no-referrer" /></svelte:head>

<div class="flex min-h-[calc(100vh-4rem)] items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center"><BrandLogo className="mx-auto mb-4 h-9 w-auto max-w-56" /><h1 class="text-2xl font-semibold tracking-tight text-stone-100">Set a new password</h1><p class="mt-1.5 text-sm text-stone-500">{eventName}</p></div>
		<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-5 sm:p-6">
			{#if loading}
				<div class="flex items-center justify-center gap-2 py-8 text-sm text-stone-500"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />Checking reset link…</div>
			{:else if complete}
				<div class="text-center"><Icon icon="mdi:check-circle-outline" class="mx-auto h-9 w-9 text-emerald-400" /><h2 class="mt-3 text-base font-medium text-stone-100">Password updated</h2><p class="mt-2 text-sm text-stone-500">All existing sessions were signed out.</p><a href="/login" class="mt-5 inline-flex w-full items-center justify-center rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950">Continue to sign in</a></div>
			{:else if error && !username}
				<div class="text-center"><Icon icon="mdi:link-variant-off" class="mx-auto h-9 w-9 text-down" /><p class="mt-3 text-sm text-down">{error}</p><a href="/forgot-password" class="mt-4 inline-flex text-sm text-stone-300 hover:text-stone-100">Request a new link</a></div>
			{:else}
				<form class="space-y-4" on:submit|preventDefault={resetPassword}>
					<div class="rounded-md border border-stone-800 bg-stone-950/50 px-3 py-2.5"><p class="text-[10px] uppercase tracking-wider text-stone-600">Account</p><p class="mt-1 text-sm font-medium text-stone-200">{displayName}</p><p class="text-xs text-stone-500">{username}</p></div>
					{#if error}<p class="flex items-start gap-1.5 text-sm text-down"><Icon icon="mdi:alert-circle-outline" class="mt-0.5 h-4 w-4 shrink-0" />{error}</p>{/if}
					<label class="block"><span class="mb-1.5 block text-sm font-medium text-stone-300">New password</span><input type="password" autocomplete="new-password" minlength="8" maxlength="72" bind:value={password} required class="w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-600" /></label>
					<label class="block"><span class="mb-1.5 block text-sm font-medium text-stone-300">Confirm password</span><input type="password" autocomplete="new-password" minlength="8" maxlength="72" bind:value={confirmation} required class="w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-600" /></label>
					<button type="submit" disabled={submitting} class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-50"><Icon icon={submitting ? 'mdi:loading' : 'mdi:lock-reset'} class="h-4 w-4 {submitting ? 'animate-spin' : ''}" />{submitting ? 'Updating…' : 'Update password'}</button>
				</form>
			{/if}
		</div>
	</div>
</div>
