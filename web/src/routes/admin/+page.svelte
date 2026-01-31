<script lang="ts">
import { onMount } from 'svelte';
import { goto } from '$app/navigation';
import { api } from '$api';
import { auth } from '$stores/auth';
import Icon from '@iconify/svelte';

let activeTab = 'overview';
let loading = true;
let stats: any = null;
let users: any[] = [];
let challenges: any[] = [];
let error = '';
let showCreateModal = false;
let showEditModal = false;
let editingChallenge: any = null;
let actionLoading = '';

// Challenge creation
let newChallenge = {
name: '',
description: '',
category: '',
difficulty: 'easy',
base_points: 100,
flag: '',
flags: [{ name: 'User Flag', flag: '', points: 50 }, { name: 'Root Flag', flag: '', points: 50 }],
type: 'container', // container, ova, static
docker_image: '',
ova_url: '',
files: []
};
let uploadLoading = false;
let uploadError = '';
let ovaFile: File | null = null;
let uploadProgress = 0;

function addFlag() {
  newChallenge.flags = [...newChallenge.flags, { name: `Flag ${newChallenge.flags.length + 1}`, flag: '', points: 25 }];
}

function removeFlag(index: number) {
  newChallenge.flags = newChallenge.flags.filter((_, i) => i !== index);
}

async function handleOvaUpload(event: Event) {
  const input = event.target as HTMLInputElement;
  if (input.files && input.files[0]) {
    ovaFile = input.files[0];
  }
}

async function publishChallenge(challenge: any) {
  actionLoading = challenge.id;
  try {
    await api.publishChallenge(challenge.id);
    await loadDashboard();
  } catch (e) {
    alert(e instanceof Error ? e.message : 'Failed to publish challenge');
  } finally {
    actionLoading = '';
  }
}

async function unpublishChallenge(challenge: any) {
  actionLoading = challenge.id;
  try {
    await api.updateAdminChallenge(challenge.id, { ...challenge, status: 'draft' });
    await loadDashboard();
  } catch (e) {
    alert(e instanceof Error ? e.message : 'Failed to unpublish challenge');
  } finally {
    actionLoading = '';
  }
}

async function deleteChallenge(challenge: any) {
  if (!confirm(`Are you sure you want to delete "${challenge.name}"? This action cannot be undone.`)) return;
  actionLoading = challenge.id;
  try {
    await api.deleteAdminChallenge(challenge.id);
    await loadDashboard();
  } catch (e) {
    alert(e instanceof Error ? e.message : 'Failed to delete challenge');
  } finally {
    actionLoading = '';
  }
}

function openEditModal(challenge: any) {
  editingChallenge = { ...challenge };
  showEditModal = true;
}

async function handleEditChallenge() {
  if (!editingChallenge) return;
  actionLoading = editingChallenge.id;
  try {
    await api.updateAdminChallenge(editingChallenge.id, {
      name: editingChallenge.name,
      description: editingChallenge.description,
      difficulty: editingChallenge.difficulty,
      base_points: editingChallenge.base_points,
    });
    showEditModal = false;
    editingChallenge = null;
    await loadDashboard();
  } catch (e) {
    alert(e instanceof Error ? e.message : 'Failed to update challenge');
  } finally {
    actionLoading = '';
  }
}

onMount(async () => {
// Wait for auth to be checked
await auth.checkAuth();

if (!$auth.isAuthenticated || $auth.user?.role !== 'admin') {
goto('/');
return;
}
await loadDashboard();
});

async function loadDashboard() {
try {
const [statsRes, usersRes, challengesRes] = await Promise.all([
api.getAdminStats(),
api.getAdminUsers(),
api.getAdminChallenges()
]);

stats = statsRes;
users = usersRes.users || [];
challenges = challengesRes.challenges || [];
} catch (e) {
error = e instanceof Error ? e.message : 'Failed to load dashboard';
} finally {
loading = false;
}
}

function formatDate(dateString: string): string {
const date = new Date(dateString);
return date.toLocaleDateString('en-US', { 
month: 'short', 
day: 'numeric',
year: 'numeric'
});
}

async function handleCreateChallenge() {
  uploadLoading = true;
  uploadError = '';
  uploadProgress = 0;

  try {
    if (newChallenge.type === 'ova') {
      // OVA upload with progress
      if (!ovaFile) {
        uploadError = 'Please select an OVA file';
        uploadLoading = false;
        return;
      }

      const formData = new FormData();
      formData.append('file', ovaFile);
      formData.append('name', newChallenge.name);
      formData.append('description', newChallenge.description);
      formData.append('difficulty', newChallenge.difficulty);
      formData.append('base_points', newChallenge.base_points.toString());
      formData.append('flags', JSON.stringify(newChallenge.flags));

      await api.uploadOvaChallenge(formData, (progress: number) => {
        uploadProgress = progress;
      });
    } else {
      // Docker container challenge
      await api.createAdminChallenge({
        name: newChallenge.name,
        description: newChallenge.description,
        difficulty: newChallenge.difficulty,
        base_points: newChallenge.base_points,
        challenge_type: 'docker',
        container_image: newChallenge.docker_image,
        flag: newChallenge.flag
      });
    }

    showCreateModal = false;
    ovaFile = null;
    uploadProgress = 0;
    await loadDashboard();
    
    // Reset form
    newChallenge = {
      name: '',
      description: '',
      category: '',
      difficulty: 'easy',
      base_points: 100,
      flag: '',
      flags: [{ name: 'User Flag', flag: '', points: 50 }, { name: 'Root Flag', flag: '', points: 50 }],
      type: 'container',
      docker_image: '',
      ova_url: '',
      files: []
    };
  } catch (e) {
    uploadError = e instanceof Error ? e.message : 'Failed to create challenge';
  } finally {
    uploadLoading = false;
  }
}
</script>

<svelte:head>
<title>Admin Dashboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-12">
<!-- Header -->
<div class="mb-10">
\t<div class="flex items-center gap-4 mb-3">
\t\t<div class="w-14 h-14 bg-gradient-to-br from-yellow-500 to-orange-600 rounded-2xl flex items-center justify-center shadow-lg">
\t\t\t<Icon icon="mdi:shield-crown" class="w-8 h-8 text-white" />
\t\t</div>
\t\t<div>
\t\t\t<h1 class="text-4xl font-bold text-white tracking-tight">Admin Dashboard</h1>
\t\t\t<p class="text-stone-400 text-sm mt-1">Manage challenges, users, and platform settings</p>
\t\t</div>
\t</div>
</div>

{#if loading}
<div class="flex items-center justify-center min-h-[40vh]">
<Icon icon="mdi:loading" class="w-12 h-12 text-stone-500 animate-spin" />
</div>
{:else if error}
<div class="bg-red-950/30 border border-red-800 rounded-xl p-6 text-center">
<Icon icon="mdi:alert-circle" class="w-12 h-12 text-red-400 mx-auto mb-3" />
<p class="text-red-300 font-medium">{error}</p>
</div>
{:else}
<!-- Tabs -->
<div class="flex space-x-3 mb-8 bg-stone-950/50 backdrop-blur-sm border border-stone-800/50 rounded-xl p-2">
	{#each [
		{ id: 'overview', label: 'Overview', icon: 'mdi:view-dashboard' },
		{ id: 'challenges', label: 'Challenges', icon: 'mdi:flag' },
		{ id: 'users', label: 'Users', icon: 'mdi:account-group' }
	] as tab}
		<button
			type="button"
			on:click={() => activeTab = tab.id}
			class="flex items-center gap-2 px-6 py-3 rounded-lg text-sm font-semibold transition-all {activeTab === tab.id ? 'bg-white text-black shadow-lg' : 'text-stone-400 hover:text-stone-200 hover:bg-stone-900/50'}"
		>
			<Icon icon={tab.icon} class="w-5 h-5" />
			{tab.label}
		</button>
	{/each}
</div>

<!-- Overview Tab -->
{#if activeTab === 'overview'}
		<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-5 mb-8">
			<!-- Stat Cards -->
			{#each [
				{ label: 'Total Users', value: stats?.total_users || 0, icon: 'mdi:account-group', gradient: 'from-blue-500 to-cyan-500' },
				{ label: 'Challenges', value: stats?.total_challenges || 0, icon: 'mdi:flag', gradient: 'from-green-500 to-emerald-500' },
				{ label: 'Active Instances', value: stats?.active_instances || 0, icon: 'mdi:server', gradient: 'from-yellow-500 to-orange-500' },
				{ label: 'Total Solves', value: stats?.total_solves || 0, icon: 'mdi:check-circle', gradient: 'from-purple-500 to-pink-500' }
			] as stat}
				<div class="bg-stone-950/50 backdrop-blur-sm border border-stone-800/50 rounded-2xl p-6 hover:border-stone-700/50 transition-all group">
					<div class="flex items-center justify-between mb-4">
						<div class="w-12 h-12 bg-gradient-to-br {stat.gradient} rounded-xl flex items-center justify-center shadow-lg">
							<Icon icon={stat.icon} class="w-7 h-7 text-white" />
						</div>
					</div>
					<p class="text-stone-400 text-sm font-medium mb-2">{stat.label}</p>
					<p class="text-4xl font-bold text-white tracking-tight">{stat.value}</p>
				</div>
			{/each}
		</div>

<!-- Quick Actions -->
<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
<div class="bg-stone-950 border border-stone-800 rounded-xl p-6">
<h2 class="text-xl font-bold text-white mb-4 flex items-center gap-2">
<Icon icon="mdi:account-plus" class="w-6 h-6" />
Recent Registrations
</h2>
<div class="space-y-3">
{#each users.slice(0, 5) as user}
<div class="flex items-center justify-between p-4 bg-black border border-stone-800 rounded-lg hover:border-stone-700 transition-colors">
<div class="flex items-center gap-3">
<div class="w-10 h-10 bg-gradient-to-br from-stone-700 to-stone-800 rounded-full flex items-center justify-center">
<span class="text-sm font-bold text-white">{user.username.charAt(0).toUpperCase()}</span>
</div>
<div>
<p class="text-white font-medium">{user.username}</p>
<p class="text-stone-500 text-sm">{user.email}</p>
</div>
</div>
<span class="text-stone-400 text-sm">{formatDate(user.created_at)}</span>
</div>
{/each}
</div>
</div>

<div class="bg-stone-950 border border-stone-800 rounded-xl p-6">
<h2 class="text-xl font-bold text-white mb-4 flex items-center gap-2">
<Icon icon="mdi:chart-line" class="w-6 h-6" />
Popular Challenges
</h2>
<div class="space-y-3">
{#each challenges.slice(0, 5) as challenge}
<div class="flex items-center justify-between p-4 bg-black border border-stone-800 rounded-lg hover:border-stone-700 transition-colors">
<div class="flex items-center gap-3">
<Icon icon="mdi:flag" class="w-6 h-6 text-stone-400" />
<div>
<p class="text-white font-medium">{challenge.name}</p>
<p class="text-stone-500 text-sm capitalize">{challenge.difficulty} • {challenge.category}</p>
</div>
</div>
<div class="text-right">
<p class="text-white font-bold">{challenge.total_solves || 0}</p>
<p class="text-stone-500 text-xs">solves</p>
</div>
</div>
{/each}
</div>
</div>
</div>
{/if}

<!-- Challenges Tab -->
{#if activeTab === 'challenges'}
<div class="bg-stone-950 border border-stone-800 rounded-xl overflow-hidden">
<div class="p-6 border-b border-stone-800 flex items-center justify-between">
<h2 class="text-2xl font-bold text-white flex items-center gap-2">
<Icon icon="mdi:flag" class="w-6 h-6" />
Challenges ({challenges.length})
</h2>
<button
on:click={() => showCreateModal = true}
class="px-6 py-3 bg-white text-black font-bold rounded-xl hover:bg-stone-200 transition-all flex items-center gap-2 transform hover:scale-105"
>
<Icon icon="mdi:plus-circle" class="w-5 h-5" />
Create Challenge
</button>
</div>

<div class="overflow-x-auto">
<table class="w-full">
<thead class="bg-black border-b border-stone-800">
<tr>
<th class="px-6 py-4 text-left text-xs font-bold text-stone-400 uppercase tracking-wider">Challenge</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Difficulty</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Type</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Points</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Solves</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Status</th>
<th class="px-6 py-4 text-right text-xs font-bold text-stone-400 uppercase tracking-wider">Actions</th>
</tr>
</thead>
<tbody class="divide-y divide-stone-800">
{#each challenges as challenge}
<tr class="hover:bg-black transition-colors">
<td class="px-6 py-4">
<div>
<p class="text-white font-semibold">{challenge.name}</p>
<p class="text-stone-500 text-sm">{challenge.total_flags || 1} flag(s)</p>
</div>
</td>
<td class="px-6 py-4 text-center">
<span class="px-3 py-1 rounded-full text-xs font-bold capitalize
{challenge.difficulty === 'easy' ? 'bg-green-950 text-green-400 border border-green-800' :
 challenge.difficulty === 'medium' ? 'bg-yellow-950 text-yellow-400 border border-yellow-800' :
 'bg-red-950 text-red-400 border border-red-800'}">
{challenge.difficulty}
</span>
</td>
<td class="px-6 py-4 text-center">
<span class="px-3 py-1 bg-stone-900 text-stone-400 text-xs font-medium border border-stone-800 rounded-lg">
{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}
</span>
</td>
<td class="px-6 py-4 text-center text-white font-bold">{challenge.base_points}</td>
<td class="px-6 py-4 text-center text-stone-300">{challenge.total_solves || 0}</td>
<td class="px-6 py-4 text-center">
{#if challenge.status === 'published'}
<span class="px-3 py-1 bg-green-950 text-green-400 text-xs font-bold border border-green-800 rounded-full">
PUBLISHED
</span>
{:else if challenge.status === 'draft'}
<span class="px-3 py-1 bg-yellow-950 text-yellow-400 text-xs font-bold border border-yellow-800 rounded-full">
DRAFT
</span>
{:else}
<span class="px-3 py-1 bg-stone-900 text-stone-500 text-xs font-bold border border-stone-800 rounded-full">
{challenge.status?.toUpperCase() || 'UNKNOWN'}
</span>
{/if}
</td>
<td class="px-6 py-4 text-right">
<div class="flex items-center justify-end gap-1">
{#if challenge.status === 'published'}
<button 
  on:click={() => unpublishChallenge(challenge)}
  disabled={actionLoading === challenge.id}
  class="text-yellow-400 hover:text-yellow-300 transition-colors p-2 hover:bg-stone-900 rounded-lg disabled:opacity-50"
  title="Unpublish"
>
  {#if actionLoading === challenge.id}
    <Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
  {:else}
    <Icon icon="mdi:eye-off" class="w-5 h-5" />
  {/if}
</button>
{:else}
<button 
  on:click={() => publishChallenge(challenge)}
  disabled={actionLoading === challenge.id}
  class="text-green-400 hover:text-green-300 transition-colors p-2 hover:bg-stone-900 rounded-lg disabled:opacity-50"
  title="Publish"
>
  {#if actionLoading === challenge.id}
    <Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
  {:else}
    <Icon icon="mdi:eye" class="w-5 h-5" />
  {/if}
</button>
{/if}
<button 
  on:click={() => openEditModal(challenge)}
  class="text-stone-400 hover:text-white transition-colors p-2 hover:bg-stone-900 rounded-lg"
  title="Edit"
>
<Icon icon="mdi:pencil" class="w-5 h-5" />
</button>
<button 
  on:click={() => deleteChallenge(challenge)}
  disabled={actionLoading === challenge.id}
  class="text-stone-400 hover:text-red-400 transition-colors p-2 hover:bg-stone-900 rounded-lg disabled:opacity-50"
  title="Delete"
>
<Icon icon="mdi:delete" class="w-5 h-5" />
</button>
</div>
</td>
</tr>
{/each}
</tbody>
</table>
</div>
</div>
{/if}

<!-- Users Tab -->
{#if activeTab === 'users'}
<div class="bg-stone-950 border border-stone-800 rounded-xl overflow-hidden">
<div class="p-6 border-b border-stone-800 flex items-center justify-between">
<h2 class="text-2xl font-bold text-white flex items-center gap-2">
<Icon icon="mdi:account-group" class="w-6 h-6" />
Users ({users.length})
</h2>
</div>

<div class="overflow-x-auto">
<table class="w-full">
<thead class="bg-black border-b border-stone-800">
<tr>
<th class="px-6 py-4 text-left text-xs font-bold text-stone-400 uppercase tracking-wider">User</th>
<th class="px-6 py-4 text-left text-xs font-bold text-stone-400 uppercase tracking-wider">Email</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Role</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Points</th>
<th class="px-6 py-4 text-center text-xs font-bold text-stone-400 uppercase tracking-wider">Solves</th>
<th class="px-6 py-4 text-right text-xs font-bold text-stone-400 uppercase tracking-wider">Actions</th>
</tr>
</thead>
<tbody class="divide-y divide-stone-800">
{#each users as user}
<tr class="hover:bg-black transition-colors">
<td class="px-6 py-4">
<div class="flex items-center gap-3">
<div class="w-10 h-10 bg-gradient-to-br from-stone-700 to-stone-800 rounded-full flex items-center justify-center">
<span class="text-sm font-bold text-white">{user.username.charAt(0).toUpperCase()}</span>
</div>
<div>
<p class="text-white font-semibold">{user.username}</p>
{#if user.display_name}
<p class="text-stone-500 text-sm">{user.display_name}</p>
{/if}
</div>
</div>
</td>
<td class="px-6 py-4 text-stone-300">{user.email}</td>
<td class="px-6 py-4 text-center">
{#if user.role === 'admin'}
<span class="px-3 py-1 bg-yellow-950 text-yellow-400 text-xs font-bold border border-yellow-800 rounded-full">
ADMIN
</span>
{:else}
<span class="px-3 py-1 bg-stone-900 text-stone-400 text-xs font-medium border border-stone-800 rounded-full">
USER
</span>
{/if}
</td>
<td class="px-6 py-4 text-center text-white font-bold">{user.total_points || 0}</td>
<td class="px-6 py-4 text-center text-stone-300">{user.total_solves || 0}</td>
<td class="px-6 py-4 text-right space-x-2">
<button class="text-stone-400 hover:text-white transition-colors p-2 hover:bg-stone-900 rounded-lg">
<Icon icon="mdi:pencil" class="w-5 h-5" />
</button>
<button class="text-stone-400 hover:text-red-400 transition-colors p-2 hover:bg-stone-900 rounded-lg">
<Icon icon="mdi:delete" class="w-5 h-5" />
</button>
</td>
</tr>
{/each}
</tbody>
</table>
</div>
</div>
{/if}
{/if}
</div>

<!-- Create Challenge Modal -->
{#if showCreateModal}
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div 
	class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4" 
	on:click={() => showCreateModal = false}
	role="dialog"
	aria-modal="true"
	aria-labelledby="modal-title"
>
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="bg-stone-950 border border-stone-800 rounded-2xl max-w-2xl w-full shadow-2xl max-h-[90vh] flex flex-col" on:click|stopPropagation>
<div class="p-6 border-b border-stone-800 flex items-center justify-between flex-shrink-0">
<h2 id="modal-title" class="text-2xl font-bold text-white flex items-center gap-2">
<Icon icon="mdi:plus-circle" class="w-6 h-6" />
Create New Challenge
</h2>
<button type="button" on:click={() => showCreateModal = false} class="text-stone-400 hover:text-white transition-colors">
<Icon icon="mdi:close" class="w-6 h-6" />
</button>
</div>

<div class="overflow-y-auto flex-1 min-h-0">
<!-- Upload Progress Bar -->
{#if uploadLoading && uploadProgress > 0}
<div class="px-6 py-3 bg-stone-900/50 border-b border-stone-800">
  <div class="flex items-center justify-between mb-2">
    <span class="text-sm text-stone-300 font-medium">Uploading OVA...</span>
    <span class="text-sm text-stone-400">{uploadProgress}%</span>
  </div>
  <div class="w-full bg-stone-800 rounded-full h-2 overflow-hidden">
    <div class="bg-gradient-to-r from-green-500 to-emerald-400 h-full transition-all duration-300 ease-out" style="width: {uploadProgress}%"></div>
  </div>
</div>
{/if}

<form on:submit|preventDefault={handleCreateChallenge} class="p-6 space-y-6">
{#if uploadError}
<div class="bg-red-950/30 border border-red-800 rounded-lg px-4 py-3 text-red-300 text-sm font-medium flex items-center gap-2">
<Icon icon="mdi:alert-circle" class="w-5 h-5" />
{uploadError}
</div>
{/if}

<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Challenge Name *</label>
<input
type="text"
bind:value={newChallenge.name}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
placeholder="Enter challenge name"
/>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Category *</label>
<input
type="text"
bind:value={newChallenge.category}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
placeholder="Web, Crypto, Pwn..."
/>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Difficulty *</label>
<select
bind:value={newChallenge.difficulty}
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
>
<option value="easy">Easy</option>
<option value="medium">Medium</option>
<option value="hard">Hard</option>
</select>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Base Points *</label>
<input
type="number"
bind:value={newChallenge.base_points}
required
min="1"
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
placeholder="100"
/>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Challenge Type *</label>
<select
bind:value={newChallenge.type}
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
>
<option value="container">Docker Container</option>
<option value="ova">OVA/VM Image</option>
<option value="static">Static Files</option>
</select>
</div>

<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Description *</label>
<textarea
bind:value={newChallenge.description}
required
rows="4"
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all resize-none"
placeholder="Challenge description..."
></textarea>
</div>

{#if newChallenge.type === 'container'}
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Docker Image *</label>
<input
type="text"
bind:value={newChallenge.docker_image}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
placeholder="registry.example.com/challenge:latest"
/>
<p class="text-stone-500 text-xs mt-2">Docker image should be pre-built and pushed to a registry</p>
</div>
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Flag *</label>
<input
type="text"
bind:value={newChallenge.flag}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all font-mono"
placeholder={'flag{example_flag_here}'}
/>
</div>
{:else if newChallenge.type === 'ova'}
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">OVA File *</label>
<div class="border-2 border-dashed border-stone-700 rounded-xl p-6 text-center hover:border-stone-500 transition-colors">
{#if ovaFile}
<div class="flex items-center justify-center gap-3">
<Icon icon="mdi:file-check" class="w-8 h-8 text-green-400" />
<div class="text-left">
<p class="text-white font-medium">{ovaFile.name}</p>
<p class="text-stone-500 text-sm">{(ovaFile.size / 1024 / 1024 / 1024).toFixed(2)} GB</p>
</div>
<button type="button" on:click={() => ovaFile = null} class="text-red-400 hover:text-red-300 p-2">
<Icon icon="mdi:close" class="w-5 h-5" />
</button>
</div>
{:else}
<Icon icon="mdi:cloud-upload" class="w-12 h-12 text-stone-500 mx-auto mb-3" />
<p class="text-stone-400 mb-2">Drop your OVA file here or click to browse</p>
<input type="file" accept=".ova,.qcow2,.vmdk" on:change={handleOvaUpload} class="hidden" id="ova-upload" />
<label for="ova-upload" class="inline-block px-4 py-2 bg-stone-800 text-white rounded-lg cursor-pointer hover:bg-stone-700 transition-colors">
Select File
</label>
{/if}
</div>
<p class="text-stone-500 text-xs mt-2">Supported formats: .ova, .qcow2, .vmdk (max 20GB)</p>
</div>

<!-- Multiple Flags for OVA -->
<div class="md:col-span-2">
<div class="flex items-center justify-between mb-3">
<label class="block text-sm font-semibold text-stone-200">Flags ({newChallenge.flags.length})</label>
<button type="button" on:click={addFlag} class="text-sm text-stone-400 hover:text-white flex items-center gap-1">
<Icon icon="mdi:plus" class="w-4 h-4" />
Add Flag
</button>
</div>
<div class="space-y-3">
{#each newChallenge.flags as flag, i}
<div class="bg-black border border-stone-700 rounded-xl p-4">
<div class="flex items-center gap-3 mb-3">
<input
type="text"
bind:value={flag.name}
class="flex-1 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm"
placeholder="Flag name (e.g., User Flag)"
/>
<input
type="number"
bind:value={flag.points}
min="1"
class="w-24 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm text-center"
placeholder="Points"
/>
{#if newChallenge.flags.length > 1}
<button type="button" on:click={() => removeFlag(i)} class="text-red-400 hover:text-red-300 p-2">
<Icon icon="mdi:trash-can" class="w-5 h-5" />
</button>
{/if}
</div>
<input
type="text"
bind:value={flag.flag}
required
class="w-full px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white font-mono text-sm"
placeholder={'flag{...}'}
/>
</div>
{/each}
</div>
<p class="text-stone-500 text-xs mt-2">Total points: {newChallenge.flags.reduce((sum, f) => sum + (f.points || 0), 0)}</p>
</div>
{:else}
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Flag *</label>
<input
type="text"
bind:value={newChallenge.flag}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all font-mono"
placeholder={'flag{example_flag_here}'}
/>
</div>
{/if}
</div>

<div class="flex items-center gap-4 pt-4 border-t border-stone-800">
<button
type="submit"
disabled={uploadLoading}
class="flex-1 px-6 py-3.5 bg-white text-black font-bold rounded-xl hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition-all transform hover:scale-[1.02] active:scale-[0.98] flex items-center justify-center gap-2"
>
{#if uploadLoading}
<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
{#if uploadProgress > 0}Uploading... {uploadProgress}%{:else}Creating...{/if}
{:else}
<Icon icon="mdi:check-circle" class="w-5 h-5" />
Create Challenge
{/if}
</button>
<button
type="button"
on:click={() => showCreateModal = false}
disabled={uploadLoading}
class="px-6 py-3.5 bg-stone-900 text-stone-300 font-semibold rounded-xl hover:bg-stone-800 transition-all border border-stone-800 disabled:opacity-50"
>
Cancel
</button>
</div>
</form>
</div>
</div>
</div>
</div>
{/if}

<!-- Edit Challenge Modal -->
{#if showEditModal && editingChallenge}
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div 
	class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4" 
	on:click={() => { showEditModal = false; editingChallenge = null; }}
	role="dialog"
	aria-modal="true"
>
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="bg-stone-950 border border-stone-800 rounded-2xl max-w-2xl w-full shadow-2xl max-h-[90vh] flex flex-col" on:click|stopPropagation>
<div class="p-6 border-b border-stone-800 flex items-center justify-between flex-shrink-0">
<h2 class="text-2xl font-bold text-white flex items-center gap-2">
<Icon icon="mdi:pencil" class="w-6 h-6" />
Edit Challenge
</h2>
<button type="button" on:click={() => { showEditModal = false; editingChallenge = null; }} class="text-stone-400 hover:text-white transition-colors">
<Icon icon="mdi:close" class="w-6 h-6" />
</button>
</div>

<div class="overflow-y-auto flex-1 min-h-0">
<form on:submit|preventDefault={handleEditChallenge} class="p-6 space-y-6">
<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Challenge Name *</label>
<input
type="text"
bind:value={editingChallenge.name}
required
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
/>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Difficulty *</label>
<select
bind:value={editingChallenge.difficulty}
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
>
<option value="easy">Easy</option>
<option value="medium">Medium</option>
<option value="hard">Hard</option>
</select>
</div>

<div>
<label class="block text-sm font-semibold text-stone-200 mb-2">Base Points *</label>
<input
type="number"
bind:value={editingChallenge.base_points}
required
min="1"
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all"
/>
</div>

<div class="md:col-span-2">
<label class="block text-sm font-semibold text-stone-200 mb-2">Description</label>
<textarea
bind:value={editingChallenge.description}
rows="4"
class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/50 transition-all resize-none"
placeholder="Challenge description..."
></textarea>
</div>

<div class="md:col-span-2">
<div class="flex items-center gap-4 p-4 bg-stone-900/50 rounded-xl border border-stone-800">
<div class="flex-1">
<p class="text-stone-200 font-medium">Challenge Status</p>
<p class="text-stone-500 text-sm">Current: <span class="font-medium {editingChallenge.status === 'published' ? 'text-green-400' : 'text-yellow-400'}">{editingChallenge.status}</span></p>
</div>
<div class="flex items-center gap-2">
<span class="text-stone-400 text-sm">Type:</span>
<span class="px-3 py-1 bg-stone-800 text-stone-300 text-sm rounded-lg">{editingChallenge.resource_type === 'vm' ? 'VM' : 'Docker'}</span>
</div>
</div>
</div>
</div>

<div class="flex items-center gap-4 pt-4 border-t border-stone-800">
<button
type="submit"
disabled={actionLoading === editingChallenge.id}
class="flex-1 px-6 py-3.5 bg-white text-black font-bold rounded-xl hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition-all flex items-center justify-center gap-2"
>
{#if actionLoading === editingChallenge.id}
<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
Saving...
{:else}
<Icon icon="mdi:check-circle" class="w-5 h-5" />
Save Changes
{/if}
</button>
<button
type="button"
on:click={() => { showEditModal = false; editingChallenge = null; }}
class="px-6 py-3.5 bg-stone-900 text-stone-300 font-semibold rounded-xl hover:bg-stone-800 transition-all border border-stone-800"
>
Cancel
</button>
</div>
</form>
</div>
</div>
</div>
</div>
{/if}
