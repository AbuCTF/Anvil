<script lang="ts">
	import Icon from '@iconify/svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { auth } from '$lib/stores/auth';

	let username = '';
	let email = '';
	let password = '';
	let confirmPassword = '';
	let loading = false;
	let error = '';

	$: usernameValid = username.length >= 3 && username.length <= 32 && /^[a-zA-Z0-9_-]+$/.test(username);
	$: emailValid = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email);
	$: passwordValid = password.length >= 8;
	$: passwordsMatch = password === confirmPassword;
	$: formValid = usernameValid && emailValid && passwordValid && passwordsMatch;

	async function handleSubmit() {
		if (!formValid) return;

		loading = true;
		error = '';

		try {
			const response = await api.register(username, email, password);

			localStorage.setItem('accessToken', response.access_token);
			localStorage.setItem('refreshToken', response.refresh_token);

			auth.login(response.access_token, response.user);

			await goto('/challenges', { replaceState: true });
		} catch (e) {
			error = e instanceof Error ? e.message : 'Registration failed';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Register - Anvil</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="text-center mb-6">
			<img src="/logo.png" alt="Anvil" class="h-9 w-auto mx-auto mb-4" />
			<h1 class="text-2xl font-bold text-stone-100 tracking-tight">Create Account</h1>
			<p class="text-sm text-stone-500 mt-1.5">
				Already have an account?
				<a href="/login" class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Sign in</a>
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
						required
						placeholder="Enter username"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if username && !usernameValid}
						<p class="mt-1.5 text-xs text-stone-500 tabular-nums">3–32 characters, letters, numbers, _ or -</p>
					{/if}
				</div>

				<div>
					<label for="email" class="block text-sm font-medium text-stone-300 mb-1.5">Email</label>
					<input
						id="email"
						type="email"
						autocomplete="email"
						bind:value={email}
						required
						placeholder="you@example.com"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if email && !emailValid}
						<p class="mt-1.5 text-xs text-stone-500">Enter a valid email address</p>
					{/if}
				</div>

				<div>
					<label for="password" class="block text-sm font-medium text-stone-300 mb-1.5">Password</label>
					<input
						id="password"
						type="password"
						autocomplete="new-password"
						bind:value={password}
						required
						placeholder="Minimum 8 characters"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if password && !passwordValid}
						<p class="mt-1.5 text-xs text-stone-500 tabular-nums">Minimum 8 characters required</p>
					{/if}
				</div>

				<div>
					<label for="confirmPassword" class="block text-sm font-medium text-stone-300 mb-1.5">Confirm Password</label>
					<input
						id="confirmPassword"
						type="password"
						autocomplete="new-password"
						bind:value={confirmPassword}
						required
						placeholder="Confirm your password"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if confirmPassword && !passwordsMatch}
						<p class="mt-1.5 text-xs text-danger">Passwords do not match</p>
					{/if}
				</div>

				<button
					type="submit"
					disabled={!formValid || loading}
					class="w-full rounded-md bg-stone-100 text-stone-950 font-medium py-2.5 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{#if loading}
						<span class="flex items-center justify-center gap-2">
							<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
							Creating account…
						</span>
					{:else}
						Create Account
					{/if}
				</button>
			</form>
		</div>
	</div>
</div>
