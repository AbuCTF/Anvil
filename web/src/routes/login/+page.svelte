<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import {
		platformInfo,
		loadPlatformInfo,
		registerHref,
		registrationAvailable
	} from '$lib/stores/platform';
	import BrandLogo from '$lib/components/BrandLogo.svelte';

	// SSO login goes through the H7 portal (magic-link there → "Enter competition" → SSO back to Anvil).
	// NOT the registration landing (2026.h7tex.com), which dead-ends a returning user.
	const PORTAL_SSO_URL = 'https://app.h7tex.com/portal';

	let username = '';
	let password = '';
	let loading = false;
	let error = '';
	let discordLoading = false;

	$: discordWalkin = $platformInfo?.discord_walkin ?? false;
	$: ssoEnabled = $platformInfo?.sso_enabled ?? false;

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

	async function handleSubmit() {
		if (!username || !password) {
			error = 'Please fill in all fields';
			return;
		}

		loading = true;
		error = '';
		try {
			const response = await api.login(username, password);
			localStorage.setItem('accessToken', response.access_token);
			if (response.refresh_token) localStorage.setItem('refreshToken', response.refresh_token);
			auth.login(response.access_token, response.user, response.refresh_token);
			window.location.href = response.user.role === 'admin' ? '/admin' : '/challenges';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Login failed';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Sign in · {$platformInfo?.name ?? 'Anvil'}</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="text-center mb-6">
			<BrandLogo className="h-9 w-auto max-w-56 mx-auto mb-4" />
			<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">Welcome Back</h1>
			<p class="text-sm text-stone-500 mt-1.5">Sign in to {$platformInfo?.name ?? 'Anvil'}.</p>
		</div>

		<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-5 sm:p-6">
			<form on:submit|preventDefault={handleSubmit} class="space-y-4">
				{#if error}
					<p class="flex items-start gap-1.5 text-danger text-sm">
						<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
						<span>{error}</span>
					</p>
				{/if}

				<div>
					<label for="username" class="block text-sm font-medium text-stone-300 mb-1.5">Username or email</label>
					<input
						id="username"
						type="text"
						autocomplete="username"
						bind:value={username}
						required
						placeholder="Enter your username or email"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
				</div>

				<div>
					<div class="mb-1.5 flex items-center justify-between gap-3"><label for="password" class="block text-sm font-medium text-stone-300">Password</label><a href="/forgot-password" class="text-xs text-stone-500 hover:text-stone-200">Forgot password?</a></div>
					<input
						id="password"
						type="password"
						autocomplete="current-password"
						bind:value={password}
						required
						placeholder="Enter your password"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
				</div>

				<button
					type="submit"
					disabled={loading}
					class="w-full rounded-md bg-stone-100 text-stone-950 font-medium py-2.5 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{#if loading}
						<span class="flex items-center justify-center gap-2 leading-none">
							<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
							Signing in…
						</span>
					{:else}
						Sign in
					{/if}
				</button>
			</form>

			{#if discordWalkin || ssoEnabled}
				<div class="flex items-center gap-3 my-4" aria-hidden="true">
					<div class="h-px flex-1 bg-stone-800"></div>
					<span class="text-xs uppercase tracking-wider text-stone-600">or</span>
					<div class="h-px flex-1 bg-stone-800"></div>
				</div>
			{/if}

			{#if discordWalkin}
				<button
					type="button"
					on:click={discordSignIn}
					disabled={discordLoading}
					class="w-full flex items-center justify-center gap-2 rounded-md bg-[#5865F2] text-white font-medium py-2.5 text-sm hover:bg-[#4752c4] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					<Icon icon={discordLoading ? 'mdi:loading' : 'ic:baseline-discord'} class="w-4 h-4 shrink-0 {discordLoading ? 'animate-spin' : ''}" />
					Continue with Discord
				</button>
			{/if}

			{#if ssoEnabled}
				<a
					href={PORTAL_SSO_URL}
					class="mt-3 w-full flex items-center justify-center gap-2 rounded-md bg-stone-800 text-stone-100 font-medium py-2.5 text-sm hover:bg-stone-700 transition-colors"
				>
					<Icon icon="mdi:shield-account-outline" class="w-4 h-4 shrink-0" />
					Continue with SSO
				</a>
			{/if}
		</div>

		{#if $registrationAvailable}
			<p class="text-sm text-stone-500 mt-4 text-center">
				New to Anvil?
				<a href={$registerHref} class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Create an account</a>
			</p>
		{/if}
	</div>
</div>
