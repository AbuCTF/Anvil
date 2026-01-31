<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { auth } from '$lib/stores/auth';

	let username = '';
	let email = '';
	let password = '';
	let confirmPassword = '';
	let inviteCode = '';
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
			const response = await api.register(username, email, password, inviteCode || undefined);
			
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

<div class="min-h-screen flex items-center justify-center py-12 px-4 sm:px-6 lg:px-8">
	<div class="max-w-md w-full">
		<div class="text-center mb-8">
			<a href="/" class="inline-block mb-6">
				<img src="/logo.png" alt="Anvil" class="h-12 w-auto mx-auto" />
			</a>
			<h2 class="text-2xl font-bold text-white">Create Account</h2>
			<p class="mt-2 text-stone-400 text-sm">
				Already have an account? <a href="/login" class="text-white hover:underline">Sign in</a>
			</p>
		</div>

		<form on:submit|preventDefault={handleSubmit} class="bg-stone-950 border border-stone-800 rounded-lg p-8 space-y-5">
			{#if error}
				<div class="bg-red-950/30 border border-red-900 rounded px-4 py-3 text-red-400 text-sm">
					{error}
				</div>
			{/if}

			<div>
				<label for="username" class="block text-sm font-medium text-stone-300 mb-2">
					Username
				</label>
				<input
					id="username"
					type="text"
					bind:value={username}
					required
					class="block w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					placeholder="Enter username"
				/>
				{#if username && !usernameValid}
					<p class="mt-1.5 text-xs text-stone-500">3-32 characters, alphanumeric only</p>
				{/if}
			</div>

			<div>
				<label for="email" class="block text-sm font-medium text-stone-300 mb-2">
					Email
				</label>
				<input
					id="email"
					type="email"
					bind:value={email}
					required
					class="block w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					placeholder="you@example.com"
				/>
			</div>

			<div>
				<label for="password" class="block text-sm font-medium text-stone-300 mb-2">
					Password
				</label>
				<input
					id="password"
					type="password"
					bind:value={password}
					required
					class="block w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					placeholder="Minimum 8 characters"
				/>
				{#if password && !passwordValid}
					<p class="mt-1.5 text-xs text-stone-500">Minimum 8 characters required</p>
				{/if}
			</div>

			<div>
				<label for="confirmPassword" class="block text-sm font-medium text-stone-300 mb-2">
					Confirm Password
				</label>
				<input
					id="confirmPassword"
					type="password"
					bind:value={confirmPassword}
					required
					class="block w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					placeholder="Confirm your password"
				/>
				{#if confirmPassword && password !== confirmPassword}
					<p class="mt-1.5 text-xs text-red-400">Passwords do not match</p>
				{/if}
			</div>

			<div>
				<label for="inviteCode" class="block text-sm font-medium text-stone-300 mb-2">
					Invite Code <span class="text-stone-500 text-xs">(Optional)</span>
				</label>
				<input
					id="inviteCode"
					type="text"
					bind:value={inviteCode}
					class="block w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					placeholder="Enter invite code"
				/>
			</div>

			<button
				type="submit"
				disabled={!formValid || loading}
				class="w-full px-6 py-3 bg-white text-black font-semibold rounded-lg hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition-all"
			>
				{#if loading}
					Creating account...
				{:else}
					Create Account
				{/if}
			</button>

			<p class="text-center text-xs text-stone-500">
				By creating an account, you agree to our terms and privacy policy
			</p>
		</form>
	</div>
</div>
