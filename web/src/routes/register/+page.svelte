<script lang="ts">
	import Icon from '@iconify/svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { auth } from '$lib/stores/auth';
	import Card from '$lib/components/Card.svelte';

	let username = '';
	let email = '';
	let password = '';
	let confirmPassword = '';
	let loading = false;
	let error = '';

	// Validation
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

			// Store tokens
			localStorage.setItem('accessToken', response.access_token);
			localStorage.setItem('refreshToken', response.refresh_token);

			// Update auth store
			auth.login(response.access_token, response.user);

			// Redirect to challenges
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
		<Card title="Create account" bodyClass="p-5 sm:p-6">
			<form on:submit|preventDefault={handleSubmit} class="space-y-4">
				{#if error}
					<p class="flex items-start gap-1.5 text-danger text-sm">
						<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
						<span>{error}</span>
					</p>
				{/if}

				<div>
					<label for="username" class="block text-sm font-medium text-stone-400 mb-1.5">Username</label>
					<input
						id="username"
						type="text"
						autocomplete="username"
						bind:value={username}
						required
						placeholder="username"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if username && !usernameValid}
						<p class="mt-1.5 text-xs text-stone-500 tabular-nums">3–32 characters, letters, numbers, _ or -</p>
					{/if}
				</div>

				<div>
					<label for="email" class="block text-sm font-medium text-stone-400 mb-1.5">Email</label>
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
					<label for="password" class="block text-sm font-medium text-stone-400 mb-1.5">Password</label>
					<input
						id="password"
						type="password"
						autocomplete="new-password"
						bind:value={password}
						required
						placeholder="password"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if password && !passwordValid}
						<p class="mt-1.5 text-xs text-stone-500 tabular-nums">Minimum 8 characters required</p>
					{/if}
				</div>

				<div>
					<label for="confirmPassword" class="block text-sm font-medium text-stone-400 mb-1.5">Confirm password</label>
					<input
						id="confirmPassword"
						type="password"
						autocomplete="new-password"
						bind:value={confirmPassword}
						required
						placeholder="repeat password"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
					{#if confirmPassword && !passwordsMatch}
						<p class="mt-1.5 text-xs text-danger">Passwords do not match</p>
					{/if}
				</div>

				<button
					type="submit"
					disabled={!formValid || loading}
					class="w-full rounded-md bg-amber-500 text-black font-medium py-2.5 text-sm hover:bg-amber-400 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{#if loading}
						<span class="flex items-center justify-center gap-2">
							<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
							Creating account…
						</span>
					{:else}
						Create account
					{/if}
				</button>
			</form>
		</Card>

		<p class="text-center text-stone-500 text-sm mt-4">
			Already have an account?
			<a href="/login" class="text-amber-500 hover:text-amber-400 transition-colors">Sign in</a>
		</p>
	</div>
</div>
