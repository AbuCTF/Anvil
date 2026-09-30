<script lang="ts">
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import BrandLogo from '$lib/components/BrandLogo.svelte';
	import { platformInfo } from '$lib/stores/platform';

	let email = '';
	let loading = false;
	let sent = false;
	let error = '';

	async function submit() {
		loading = true;
		error = '';
		try {
			await api.requestPasswordReset(email.trim());
			sent = true;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Password reset is temporarily unavailable.';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Reset password · {$platformInfo?.name ?? 'Anvil'}</title></svelte:head>

<div class="flex min-h-[calc(100vh-4rem)] items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="mb-6 text-center"><BrandLogo className="mx-auto mb-4 h-9 w-auto max-w-56" /><h1 class="text-2xl font-semibold tracking-tight text-stone-100">Reset password</h1><p class="mt-1.5 text-sm text-stone-500">Receive a secure, one-hour reset link.</p></div>
		<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-5 sm:p-6">
			{#if sent}
				<div class="text-center"><Icon icon="mdi:email-check-outline" class="mx-auto h-9 w-9 text-emerald-400" /><h2 class="mt-3 text-base font-medium text-stone-100">Check your inbox</h2><p class="mt-2 text-sm leading-relaxed text-stone-500">If that address belongs to an active account, a reset link is on its way.</p><a href="/login" class="mt-5 inline-flex text-sm font-medium text-stone-300 hover:text-stone-100">Return to sign in</a></div>
			{:else}
				<form class="space-y-4" on:submit|preventDefault={submit}>
					{#if error}<p class="flex items-start gap-1.5 text-sm text-down"><Icon icon="mdi:alert-circle-outline" class="mt-0.5 h-4 w-4 shrink-0" />{error}</p>{/if}
					<label class="block"><span class="mb-1.5 block text-sm font-medium text-stone-300">Account email</span><input type="email" autocomplete="email" bind:value={email} required class="w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2.5 text-sm text-stone-200 outline-none focus:border-stone-600" /></label>
					<button type="submit" disabled={loading} class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:opacity-50"><Icon icon={loading ? 'mdi:loading' : 'mdi:email-fast-outline'} class="h-4 w-4 {loading ? 'animate-spin' : ''}" />{loading ? 'Sending…' : 'Send reset link'}</button>
				</form>
			{/if}
		</div>
	</div>
</div>
