<script lang="ts">
import Icon from '@iconify/svelte';
import { onMount } from 'svelte';
import { page } from '$app/stores';
import { api } from '$api';
import { auth } from '$stores/auth';

let challenge: any = null;
let instance: any = null;
let loading = true;
let error = '';

// Flag submission
let flagInput = '';
let submitting = false;
let submitResult: { correct: boolean; message: string } | null = null;

// Instance management
let creatingInstance = false;
let instanceAction = '';

// Admin editing
let isEditing = false;
let editForm: any = null;
let saving = false;
let showEditSuccess = false;

const slug = $page.params.slug;

$: if (!slug) {
error = 'Invalid challenge';
}

$: isAdmin = $auth.isAuthenticated && $auth.user?.role === 'admin';

const difficultyColors: Record<string, string> = {
easy: 'bg-green-500/20 text-green-400 border-green-500/30',
medium: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
hard: 'bg-orange-500/20 text-orange-400 border-orange-500/30',
insane: 'bg-red-500/20 text-red-400 border-red-500/30'
};

const difficultyGradients: Record<string, string> = {
easy: 'from-green-500 to-emerald-600',
medium: 'from-yellow-500 to-amber-600',
hard: 'from-orange-500 to-red-600',
insane: 'from-red-500 to-purple-600'
};

onMount(async () => {
await loadChallenge();
if ($auth.isAuthenticated) {
await loadUserInstance();
}
});

async function loadChallenge() {
if (!slug) return;
try {
challenge = await api.getChallenge(slug as string);
editForm = {
name: challenge.name,
description: challenge.description || '',
difficulty: challenge.difficulty,
base_points: challenge.base_points,
category: challenge.category || ''
};
} catch (e) {
error = e instanceof Error ? e.message : 'Failed to load challenge';
} finally {
loading = false;
}
}

async function loadUserInstance() {
try {
const response = await api.getInstances();
instance = response.instances?.find((i: any) => 
i.challenge_slug === slug && i.status === 'running'
);
} catch (e) {
console.error('Failed to load instances', e);
}
}

async function submitFlag() {
if (!slug || !flagInput.trim()) return;

submitting = true;
submitResult = null;

try {
const result = await api.submitFlag(slug as string, flagInput.trim());
submitResult = result;
if (result.correct) {
flagInput = '';
await loadChallenge();
}
} catch (e) {
submitResult = {
correct: false,
message: e instanceof Error ? e.message : 'Submission failed'
};
} finally {
submitting = false;
}
}

async function startInstance() {
if (!slug) return;
creatingInstance = true;
try {
const result = await api.createInstance(slug as string);
instance = result.instance;
} catch (e) {
error = e instanceof Error ? e.message : 'Failed to start instance';
} finally {
creatingInstance = false;
}
}

async function extendInstance() {
if (!instance) return;
instanceAction = 'extending';
try {
const result = await api.extendInstance(instance.id);
instance = { ...instance, expires_at: result.new_expires_at };
} catch (e) {
error = e instanceof Error ? e.message : 'Failed to extend instance';
} finally {
instanceAction = '';
}
}

async function stopInstance() {
if (!instance) return;
instanceAction = 'stopping';
try {
await api.stopInstance(instance.id);
instance = null;
} catch (e) {
error = e instanceof Error ? e.message : 'Failed to stop instance';
} finally {
instanceAction = '';
}
}

async function handleSaveEdit() {
if (!challenge || !editForm) return;
saving = true;
try {
await api.updateAdminChallenge(challenge.id, {
name: editForm.name,
description: editForm.description,
difficulty: editForm.difficulty,
base_points: parseInt(editForm.base_points)
});
await loadChallenge();
isEditing = false;
showEditSuccess = true;
setTimeout(() => showEditSuccess = false, 3000);
} catch (e) {
alert(e instanceof Error ? e.message : 'Failed to save changes');
} finally {
saving = false;
}
}

async function handlePublish() {
if (!challenge) return;
saving = true;
try {
await api.publishChallenge(challenge.id);
await loadChallenge();
} catch (e) {
alert(e instanceof Error ? e.message : 'Failed to publish challenge');
} finally {
saving = false;
}
}

async function handleUnpublish() {
if (!challenge) return;
saving = true;
try {
await api.unpublishChallenge(challenge.id);
await loadChallenge();
} catch (e) {
alert(e instanceof Error ? e.message : 'Failed to unpublish challenge');
} finally {
saving = false;
}
}

function cancelEdit() {
isEditing = false;
editForm = {
name: challenge.name,
description: challenge.description || '',
difficulty: challenge.difficulty,
base_points: challenge.base_points,
category: challenge.category || ''
};
}

function formatTimeRemaining(expiresAt: number): string {
const now = Math.floor(Date.now() / 1000);
const remaining = expiresAt - now;
if (remaining <= 0) return 'Expired';

const hours = Math.floor(remaining / 3600);
const minutes = Math.floor((remaining % 3600) / 60);
return `${hours}h ${minutes}m`;
}

function copyToClipboard(text: string) {
navigator.clipboard.writeText(text);
}
</script>

<svelte:head>
<title>{challenge?.name || 'Challenge'} - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
{#if loading}
<div class="flex items-center justify-center min-h-[60vh]">
<div class="text-center">
<Icon icon="mdi:loading" class="w-12 h-12 text-stone-500 animate-spin mx-auto mb-4" />
<p class="text-stone-500">Loading challenge...</p>
</div>
</div>
{:else if error && !challenge}
<div class="max-w-4xl mx-auto px-4 py-16">
<div class="bg-red-950/30 border border-red-900 rounded-2xl p-8 text-center">
<Icon icon="mdi:alert-circle" class="w-16 h-16 text-red-500 mx-auto mb-4" />
<h2 class="text-xl font-bold text-white mb-2">Challenge Not Found</h2>
<p class="text-red-400 mb-6">{error}</p>
<a href="/challenges" class="inline-flex items-center gap-2 px-6 py-3 bg-stone-900 text-white rounded-xl hover:bg-stone-800 transition">
<Icon icon="mdi:arrow-left" class="w-5 h-5" />
Back to challenges
</a>
</div>
</div>
{:else if challenge}
<!-- Hero Section -->
<div class="relative overflow-hidden border-b border-stone-800">
<div class="absolute inset-0 bg-gradient-to-br {difficultyGradients[challenge.difficulty] || difficultyGradients.easy} opacity-5"></div>
<div class="absolute inset-0 bg-gradient-to-t from-black via-black/80 to-transparent"></div>

<div class="relative max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
<nav class="mb-6">
<a href="/challenges" class="inline-flex items-center gap-2 text-stone-400 hover:text-white transition group">
<Icon icon="mdi:arrow-left" class="w-4 h-4 group-hover:-translate-x-1 transition-transform" />
<span>Back to challenges</span>
</a>
</nav>

{#if showEditSuccess}
<div class="fixed top-4 right-4 z-50 bg-green-500/20 border border-green-500/30 text-green-400 px-4 py-3 rounded-xl flex items-center gap-2">
<Icon icon="mdi:check-circle" class="w-5 h-5" />
<span>Changes saved successfully</span>
</div>
{/if}

<div class="flex flex-col lg:flex-row lg:items-start lg:justify-between gap-6">
<div class="flex-1">
<div class="flex items-center gap-4 mb-3">
<div class="w-14 h-14 rounded-2xl flex items-center justify-center shadow-lg {challenge.resource_type === 'vm' ? 'bg-gradient-to-br from-purple-500 to-indigo-600' : 'bg-gradient-to-br from-blue-500 to-cyan-600'}">
<Icon icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'} class="w-7 h-7 text-white" />
</div>

<div class="flex-1">
<div class="flex items-center gap-3">
{#if isEditing}
<input type="text" bind:value={editForm.name} class="text-3xl font-bold bg-stone-900 border border-stone-700 rounded-lg px-3 py-1 text-white focus:outline-none focus:border-stone-500" />
{:else}
<h1 class="text-3xl font-bold text-white">{challenge.name}</h1>
{/if}
{#if challenge.is_solved}
<div class="flex items-center gap-1.5 px-3 py-1 bg-green-500/20 border border-green-500/30 rounded-full">
<Icon icon="mdi:check-circle" class="w-4 h-4 text-green-500" />
<span class="text-green-400 text-sm font-medium">Solved</span>
</div>
{/if}
</div>
{#if challenge.author_name}
<p class="text-stone-400 text-sm mt-1">Created by <span class="text-stone-300">{challenge.author_name}</span></p>
{/if}
</div>
</div>

<div class="flex flex-wrap items-center gap-2 mt-4">
{#if isEditing}
<select bind:value={editForm.difficulty} class="px-3 py-1.5 rounded-full text-sm font-medium border bg-stone-900 border-stone-700 text-white focus:outline-none">
<option value="easy">Easy</option>
<option value="medium">Medium</option>
<option value="hard">Hard</option>
<option value="insane">Insane</option>
</select>
{:else}
<span class="inline-flex items-center px-3 py-1 rounded-full text-sm font-semibold border {difficultyColors[challenge.difficulty] || difficultyColors.easy}">
{challenge.difficulty.charAt(0).toUpperCase() + challenge.difficulty.slice(1)}
</span>
{/if}

{#if challenge.category}
<span class="inline-flex items-center px-3 py-1 rounded-full text-sm font-medium bg-stone-900 text-stone-300 border border-stone-800">
<Icon icon="mdi:folder" class="w-3.5 h-3.5 mr-1.5" />
{challenge.category}
</span>
{/if}

<span class="inline-flex items-center px-3 py-1 rounded-full text-sm font-medium {challenge.resource_type === 'vm' ? 'bg-purple-500/20 text-purple-400 border border-purple-500/30' : 'bg-blue-500/20 text-blue-400 border border-blue-500/30'}">
<Icon icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'} class="w-3.5 h-3.5 mr-1.5" />
{challenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker Container'}
</span>

{#if challenge.status === 'draft'}
<span class="inline-flex items-center px-3 py-1 rounded-full text-sm font-medium bg-yellow-500/20 text-yellow-400 border border-yellow-500/30">
<Icon icon="mdi:pencil" class="w-3.5 h-3.5 mr-1.5" />
Draft
</span>
{/if}
</div>
</div>

<div class="flex gap-4">
<div class="text-center px-6 py-4 bg-stone-950 border border-stone-800 rounded-xl">
{#if isEditing}
<input type="number" bind:value={editForm.base_points} class="w-20 text-2xl font-bold bg-stone-900 border border-stone-700 rounded-lg px-2 py-1 text-amber-500 text-center focus:outline-none" />
{:else}
<div class="text-2xl font-bold text-amber-500">{challenge.base_points}</div>
{/if}
<div class="text-xs text-stone-500 uppercase tracking-wider mt-1">Points</div>
</div>
<div class="text-center px-6 py-4 bg-stone-950 border border-stone-800 rounded-xl">
<div class="text-2xl font-bold text-white">{challenge.total_solves}</div>
<div class="text-xs text-stone-500 uppercase tracking-wider mt-1">Solves</div>
</div>
<div class="text-center px-6 py-4 bg-stone-950 border border-stone-800 rounded-xl">
<div class="text-2xl font-bold text-white">{challenge.total_flags}</div>
<div class="text-xs text-stone-500 uppercase tracking-wider mt-1">Flags</div>
</div>
</div>
</div>
</div>
</div>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
<div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
<div class="lg:col-span-2 space-y-6">
{#if isAdmin}
<div class="bg-gradient-to-r from-amber-500/10 to-orange-500/10 border border-amber-500/20 rounded-xl p-4">
<div class="flex items-center justify-between">
<div class="flex items-center gap-3">
<div class="w-10 h-10 bg-amber-500/20 rounded-lg flex items-center justify-center">
<Icon icon="mdi:shield-crown" class="w-5 h-5 text-amber-500" />
</div>
<div>
<h3 class="text-white font-semibold">Admin Controls</h3>
<p class="text-stone-400 text-sm">Manage this challenge</p>
</div>
</div>
<div class="flex items-center gap-2">
{#if isEditing}
<button on:click={handleSaveEdit} disabled={saving} class="px-4 py-2 bg-green-500 text-white rounded-lg hover:bg-green-400 transition flex items-center gap-2 disabled:opacity-50">
{#if saving}<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />{:else}<Icon icon="mdi:check" class="w-4 h-4" />{/if}
Save
</button>
<button on:click={cancelEdit} class="px-4 py-2 bg-stone-800 text-stone-300 rounded-lg hover:bg-stone-700 transition">Cancel</button>
{:else}
<button on:click={() => isEditing = true} class="px-4 py-2 bg-stone-800 text-white rounded-lg hover:bg-stone-700 transition flex items-center gap-2">
<Icon icon="mdi:pencil" class="w-4 h-4" />
Edit
</button>
{#if challenge.status === 'draft'}
<button on:click={handlePublish} disabled={saving} class="px-4 py-2 bg-green-500/20 text-green-400 rounded-lg hover:bg-green-500/30 transition flex items-center gap-2 disabled:opacity-50">
{#if saving}<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />{:else}<Icon icon="mdi:eye" class="w-4 h-4" />{/if}
Publish
</button>
{:else}
<button on:click={handleUnpublish} disabled={saving} class="px-4 py-2 bg-yellow-500/20 text-yellow-400 rounded-lg hover:bg-yellow-500/30 transition flex items-center gap-2 disabled:opacity-50">
{#if saving}<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />{:else}<Icon icon="mdi:eye-off" class="w-4 h-4" />{/if}
Unpublish
</button>
{/if}
{/if}
</div>
</div>
</div>
{/if}

<div class="bg-stone-950 border border-stone-800 rounded-xl p-6">
<h2 class="text-lg font-semibold text-white flex items-center gap-2 mb-4">
<Icon icon="mdi:text" class="w-5 h-5 text-stone-500" />
Description
</h2>
{#if isEditing}
<textarea bind:value={editForm.description} rows="6" class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 resize-none" placeholder="Challenge description..."></textarea>
{:else if challenge.description}
<p class="text-stone-300 leading-relaxed whitespace-pre-wrap">{challenge.description}</p>
{:else}
<p class="text-stone-500 italic">No description provided.</p>
{/if}
</div>

{#if challenge.flags && challenge.flags.length > 0}
<div class="bg-stone-950 border border-stone-800 rounded-xl p-6">
<h2 class="text-lg font-semibold text-white flex items-center gap-2 mb-4">
<Icon icon="mdi:flag" class="w-5 h-5 text-amber-500" />
Objectives
<span class="ml-auto text-sm font-normal text-stone-400">{challenge.user_solves || 0} / {challenge.total_flags} completed</span>
</h2>

<div class="mb-6">
<div class="w-full bg-stone-900 rounded-full h-2 overflow-hidden">
<div class="h-full bg-gradient-to-r from-amber-500 to-orange-500 transition-all duration-500" style="width: {((challenge.user_solves || 0) / challenge.total_flags) * 100}%"></div>
</div>
</div>

<div class="space-y-3">
{#each challenge.flags as flag, i}
<div class="flex items-center justify-between p-4 rounded-xl transition {flag.is_solved ? 'bg-green-500/10 border border-green-500/20' : 'bg-stone-900 border border-stone-800 hover:border-stone-700'}">
<div class="flex items-center gap-4">
<div class="w-10 h-10 rounded-lg flex items-center justify-center {flag.is_solved ? 'bg-green-500/20' : 'bg-stone-800'}">
{#if flag.is_solved}
<Icon icon="mdi:check" class="w-5 h-5 text-green-500" />
{:else}
<span class="text-stone-500 font-bold">{i + 1}</span>
{/if}
</div>
<div>
<span class="text-white font-medium">{flag.name}</span>
{#if flag.is_solved}<p class="text-green-400 text-sm">Captured!</p>{/if}
</div>
</div>
<div class="flex items-center gap-3">
<span class="text-amber-500 font-bold">{flag.points}</span>
<span class="text-stone-500 text-sm">pts</span>
</div>
</div>
{/each}
</div>
</div>
{/if}

{#if challenge.hints && challenge.hints.length > 0}
<div class="bg-stone-950 border border-stone-800 rounded-xl p-6">
<h2 class="text-lg font-semibold text-white flex items-center gap-2 mb-4">
<Icon icon="mdi:lightbulb" class="w-5 h-5 text-yellow-500" />
Hints
</h2>
<div class="space-y-3">
{#each challenge.hints as hint, i}
<div class="p-4 bg-stone-900 border border-stone-800 rounded-xl">
{#if hint.is_unlocked}
<p class="text-stone-300">{hint.content}</p>
{:else}
<div class="flex items-center justify-between">
<div class="flex items-center gap-3">
<Icon icon="mdi:lock" class="w-5 h-5 text-stone-600" />
<span class="text-stone-500">Hint #{i + 1}</span>
</div>
<button class="px-4 py-2 bg-yellow-500/20 text-yellow-500 rounded-lg text-sm hover:bg-yellow-500/30 transition flex items-center gap-2">
<Icon icon="mdi:lock-open" class="w-4 h-4" />
Unlock ({hint.cost} pts)
</button>
</div>
{/if}
</div>
{/each}
</div>
</div>
{/if}
</div>

<div class="space-y-6">
{#if $auth.isAuthenticated}
<div class="bg-stone-950 border border-stone-800 rounded-xl overflow-hidden">
<div class="p-4 border-b border-stone-800 bg-stone-900/50">
<h2 class="text-lg font-semibold text-white flex items-center gap-2">
<Icon icon="mdi:server" class="w-5 h-5 text-amber-500" />
Instance
</h2>
</div>
<div class="p-4">
{#if instance}
<div class="space-y-4">
<div class="flex items-center gap-2 p-3 bg-green-500/10 border border-green-500/20 rounded-lg">
<span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
<span class="text-green-400 font-medium">Running</span>
</div>
<div class="space-y-3">
<div class="p-3 bg-stone-900 rounded-lg">
<div class="text-xs text-stone-500 uppercase tracking-wider mb-1">IP Address</div>
<div class="flex items-center justify-between">
<code class="text-amber-500 font-mono text-lg">{instance.ip_address}</code>
<button on:click={() => copyToClipboard(instance.ip_address)} class="p-2 text-stone-400 hover:text-white transition" title="Copy IP">
<Icon icon="mdi:content-copy" class="w-4 h-4" />
</button>
</div>
</div>
<div class="p-3 bg-stone-900 rounded-lg">
<div class="text-xs text-stone-500 uppercase tracking-wider mb-1">Time Remaining</div>
<div class="flex items-center gap-2">
<Icon icon="mdi:clock-outline" class="w-5 h-5 text-stone-400" />
<span class="text-white font-medium">{formatTimeRemaining(instance.expires_at)}</span>
</div>
</div>
</div>
<div class="grid grid-cols-2 gap-2">
<button on:click={extendInstance} disabled={instanceAction === 'extending'} class="px-4 py-3 bg-stone-900 text-white rounded-lg hover:bg-stone-800 transition flex items-center justify-center gap-2 disabled:opacity-50">
{#if instanceAction === 'extending'}<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />{:else}<Icon icon="mdi:clock-plus" class="w-5 h-5" />{/if}
<span>Extend</span>
</button>
<button on:click={stopInstance} disabled={instanceAction === 'stopping'} class="px-4 py-3 bg-red-500/20 text-red-400 rounded-lg hover:bg-red-500/30 transition flex items-center justify-center gap-2 disabled:opacity-50">
{#if instanceAction === 'stopping'}<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />{:else}<Icon icon="mdi:stop" class="w-5 h-5" />{/if}
<span>Stop</span>
</button>
</div>
</div>
{:else}
<div class="text-center py-4">
<div class="w-16 h-16 bg-stone-900 rounded-full flex items-center justify-center mx-auto mb-4">
<Icon icon="mdi:server-off" class="w-8 h-8 text-stone-600" />
</div>
<p class="text-stone-400 text-sm mb-4">Start an instance to begin hacking this {challenge.resource_type === 'vm' ? 'machine' : 'container'}.</p>
<button on:click={startInstance} disabled={creatingInstance} class="w-full px-4 py-4 bg-gradient-to-r from-amber-500 to-orange-500 text-black font-bold rounded-xl hover:from-amber-400 hover:to-orange-400 transition disabled:opacity-50 flex items-center justify-center gap-2 shadow-lg shadow-amber-500/25">
{#if creatingInstance}
<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
<span>Starting...</span>
{:else}
<Icon icon="mdi:play" class="w-5 h-5" />
<span>Start Instance</span>
{/if}
</button>
</div>
{/if}
</div>
</div>
{/if}

{#if $auth.isAuthenticated}
<div class="bg-stone-950 border border-stone-800 rounded-xl overflow-hidden">
<div class="p-4 border-b border-stone-800 bg-stone-900/50">
<h2 class="text-lg font-semibold text-white flex items-center gap-2">
<Icon icon="mdi:flag-checkered" class="w-5 h-5 text-amber-500" />
Submit Flag
</h2>
</div>
<div class="p-4">
<form on:submit|preventDefault={submitFlag} class="space-y-4">
<input type="text" bind:value={flagInput} placeholder="flag..." class="w-full px-4 py-4 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-600 focus:outline-none focus:border-amber-500 focus:ring-2 focus:ring-amber-500/20 font-mono transition" />
<button type="submit" disabled={submitting || !flagInput.trim()} class="w-full px-4 py-4 bg-white text-black font-bold rounded-xl hover:bg-stone-200 transition disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-2">
{#if submitting}
<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
<span>Checking...</span>
{:else}
<Icon icon="mdi:send" class="w-5 h-5" />
<span>Submit</span>
{/if}
</button>
</form>
{#if submitResult}
<div class="mt-4 p-4 rounded-xl flex items-center gap-3 {submitResult.correct ? 'bg-green-500/10 border border-green-500/20' : 'bg-red-500/10 border border-red-500/20'}">
<Icon icon={submitResult.correct ? 'mdi:check-circle' : 'mdi:close-circle'} class="w-6 h-6 flex-shrink-0 {submitResult.correct ? 'text-green-500' : 'text-red-500'}" />
<span class="{submitResult.correct ? 'text-green-400' : 'text-red-400'}">{submitResult.message}</span>
</div>
{/if}
</div>
</div>
{:else}
<div class="bg-stone-950 border border-stone-800 rounded-xl p-6 text-center">
<div class="w-16 h-16 bg-stone-900 rounded-full flex items-center justify-center mx-auto mb-4">
<Icon icon="mdi:lock" class="w-8 h-8 text-stone-600" />
</div>
<h3 class="text-white font-semibold mb-2">Authentication Required</h3>
<p class="text-stone-400 text-sm mb-4">Login to start this challenge and submit flags.</p>
<a href="/login" class="inline-flex items-center gap-2 px-6 py-3 bg-gradient-to-r from-amber-500 to-orange-500 text-black font-bold rounded-xl hover:from-amber-400 hover:to-orange-400 transition shadow-lg shadow-amber-500/25">
<Icon icon="mdi:login" class="w-5 h-5" />
Login
</a>
</div>
{/if}

<div class="bg-stone-950 border border-stone-800 rounded-xl p-4">
<h3 class="text-sm font-semibold text-stone-400 uppercase tracking-wider mb-4">Quick Info</h3>
<div class="space-y-3">
<div class="flex items-center justify-between">
<span class="text-stone-500">Type</span>
<span class="text-white">{challenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker Container'}</span>
</div>
<div class="flex items-center justify-between">
<span class="text-stone-500">Difficulty</span>
<span class="capitalize {challenge.difficulty === 'easy' ? 'text-green-400' : challenge.difficulty === 'medium' ? 'text-yellow-400' : challenge.difficulty === 'hard' ? 'text-orange-400' : 'text-red-400'}">{challenge.difficulty}</span>
</div>
<div class="flex items-center justify-between">
<span class="text-stone-500">Points</span>
<span class="text-amber-500 font-bold">{challenge.base_points}</span>
</div>
<div class="flex items-center justify-between">
<span class="text-stone-500">Solves</span>
<span class="text-white">{challenge.total_solves}</span>
</div>
<div class="flex items-center justify-between">
<span class="text-stone-500">Flags</span>
<span class="text-white">{challenge.total_flags}</span>
</div>
</div>
</div>
</div>
</div>
</div>
{/if}
</div>
