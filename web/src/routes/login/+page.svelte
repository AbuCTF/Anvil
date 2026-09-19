<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let username = '';
	let password = '';
	let loading = false;
	let error = '';

	let discordWalkin = false;
	let discordLoading = false;
	let registerUrl = '';

	onMount(async () => {
		try {
			const info = await api.getPlatformInfo();
			discordWalkin = info.discord_walkin;
			registerUrl = info.register_url ?? '';
		} catch {
			// the discord option stays hidden if platform info is unavailable
		}
	});

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
			if (response.refresh_token) {
				localStorage.setItem('refreshToken', response.refresh_token);
			}

			auth.login(response.access_token, response.user, response.refresh_token);

			if (response.user.role === 'admin') {
				window.location.href = '/admin';
			} else {
				window.location.href = '/challenges';
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Login failed';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Login - Anvil</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="text-center mb-6">
			<img src="/logo.png" alt="Anvil" class="h-9 w-auto mx-auto mb-4" />
			<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">Welcome Back</h1>
			<p class="text-sm text-stone-500 mt-1.5">
				Don't have an account?
				{#if registerUrl}
					<a href={registerUrl} class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Register</a>
				{:else}
					<a href="/register" class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Create one</a>
				{/if}
			</p>
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
					<label for="username" class="block text-sm font-medium text-stone-300 mb-1.5">Username</label>
					<input
						id="username"
						type="text"
						autocomplete="username"
						bind:value={username}
						placeholder="Enter your username"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
				</div>

				<div>
					<label for="password" class="block text-sm font-medium text-stone-300 mb-1.5">Password</label>
					<input
						id="password"
						type="password"
						autocomplete="current-password"
						bind:value={password}
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

			{#if discordWalkin}
				<button
					type="button"
					on:click={discordSignIn}
					disabled={discordLoading}
					class="mt-3 w-full flex items-center justify-center gap-2 rounded-md bg-[#5865F2] text-white font-medium py-2.5 text-sm hover:bg-[#4752c4] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					<Icon icon={discordLoading ? 'mdi:loading' : 'ic:baseline-discord'} class="w-4 h-4 shrink-0 {discordLoading ? 'animate-spin' : ''}" />
					Sign in with Discord
				</button>
			{/if}
		</div>
	</div>
</div>
