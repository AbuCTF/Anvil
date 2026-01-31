<script lang="ts">
import Icon from '@iconify/svelte';
import { api } from '$api';
import { auth } from '$stores/auth';

let username = '';
let password = '';
let loading = false;
let error = '';

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

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4">
<div class="w-full max-w-sm">
<div class="text-center mb-6">
<h2 class="text-2xl font-bold text-white">Welcome Back</h2>
<p class="mt-1 text-stone-400 text-sm">
No account? <a href="/register" class="text-white hover:underline">Create one</a>
</p>
</div>
<div class="bg-stone-950 border border-stone-800 rounded-lg p-5">
<form on:submit|preventDefault={handleSubmit} class="space-y-4">
{#if error}
<div class="bg-red-950/30 border border-red-900 rounded px-3 py-2 text-red-400 text-sm">{error}</div>
{/if}
<div>
<label for="username" class="block text-sm font-medium text-stone-300 mb-1">Username</label>
<input id="username" type="text" bind:value={username} class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white placeholder-stone-500 focus:outline-none focus:border-stone-500" placeholder="Username" />
</div>
<div>
<label for="password" class="block text-sm font-medium text-stone-300 mb-1">Password</label>
<input id="password" type="password" bind:value={password} class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white placeholder-stone-500 focus:outline-none focus:border-stone-500" placeholder="Password" />
</div>
<button type="submit" disabled={loading} class="w-full py-2.5 bg-white text-black font-semibold rounded hover:bg-stone-200 disabled:opacity-50">
{#if loading}Signing in...{:else}Sign In{/if}
</button>
</form>
<div class="my-4 border-t border-stone-800"></div>
<a href="/token" class="w-full flex justify-center py-2.5 bg-stone-900 text-stone-300 hover:bg-stone-800 border border-stone-700 rounded">Team Token Login</a>
</div>
</div>
</div>
