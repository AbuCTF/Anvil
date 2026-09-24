<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { platformInfo, loadPlatformInfo } from '$lib/stores/platform';

	let error = '';
	let discordLoading = false;

	$: discordWalkin = $platformInfo?.discord_walkin ?? false;
	$: portalUrl = $platformInfo?.register_url ?? '';

	onMount(() => loadPlatformInfo());

	async function discordSignIn() {
		discordLoading = true;
		error = '';
		try {
			const { authorize_url } = await api.discordAuthorizeUrl();
			window.location.href = authorize_url;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Discord sign-in is unavailable';
			discordLoading = false;
		}
	}
</script>

<svelte:head>
	<title>Sign in - Anvil</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="text-center mb-6">
			<img src="/logo.png" alt="Anvil" class="h-9 w-auto mx-auto mb-4" />
			<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">Sign in</h1>
			<p class="text-sm text-stone-500 mt-1.5">Use your H7 account or Discord to continue.</p>
		</div>

		<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-5 sm:p-6 space-y-3">
			{#if error}
				<p class="flex items-start gap-1.5 text-down text-sm">
					<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
					<span>{error}</span>
				</p>
			{/if}

			{#if portalUrl}
				<a
					href={portalUrl}
					class="w-full flex items-center justify-center gap-2 rounded-md bg-stone-100 text-stone-950 font-medium py-2.5 text-sm hover:bg-stone-50 transition-colors"
				>
					<Icon icon="mdi:shield-account-outline" class="w-4 h-4 shrink-0" />
					Sign in with your H7 account
				</a>
			{/if}

			{#if discordWalkin}
				<button
					type="button"
					on:click={discordSignIn}
					disabled={discordLoading}
					class="w-full flex items-center justify-center gap-2 rounded-md bg-[#5865F2] text-white font-medium py-2.5 text-sm hover:bg-[#4752c4] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					<Icon icon={discordLoading ? 'mdi:loading' : 'ic:baseline-discord'} class="w-4 h-4 shrink-0 {discordLoading ? 'animate-spin' : ''}" />
					Sign in with Discord
				</button>
			{/if}

			{#if !portalUrl && !discordWalkin}
				<p class="text-sm text-stone-500 text-center">Sign-in is temporarily unavailable.</p>
			{/if}
		</div>

		{#if portalUrl}
			<p class="text-sm text-stone-500 mt-4 text-center">
				New here?
				<a href={portalUrl} class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Register on the H7 portal</a>
			</p>
		{/if}
	</div>
</div>
