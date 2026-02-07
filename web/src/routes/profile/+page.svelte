<script lang="ts">
import { onMount } from 'svelte';
import { goto } from '$app/navigation';
import Icon from '@iconify/svelte';
import { api } from '$api';
import { auth } from '$stores/auth';

interface UserStats {
	total_score: number;
	total_solves: number;
	rank: number;
	by_difficulty: Record<string, { solved: number; total: number }>;
	by_category: Record<string, { solved: number; total: number }>;
}

interface Solve {
	challenge_name: string;
	challenge_slug: string;
	flag_name: string;
	points: number;
	solved_at: string;
}

let profile: any = null;
let stats: UserStats | null = null;
let solves: Solve[] = [];
let loading = true;
let error = '';

let editing = false;
let editForm = {
	display_name: '',
	bio: ''
};
let saving = false;
let saveError = '';

function getDifficultyColor(difficulty: string): string {
	switch(difficulty.toLowerCase()) {
		case 'easy': return 'bg-green-500';
		case 'medium': return 'bg-yellow-500';
		case 'hard': return 'bg-red-500';
		case 'insane': return 'bg-purple-500';
		default: return 'bg-stone-400';
	}
}

onMount(async () => {
	await loadProfile();
});

async function loadProfile() {
	try {
		const [profileRes, statsRes, solvesRes] = await Promise.all([
			api.getProfile(),
			api.getUserStats(),
			api.getUserSolves()
		]);

		profile = profileRes;
		stats = statsRes;
		solves = solvesRes.solves || [];

		editForm = {
			display_name: profile.display_name || '',
			bio: profile.bio || ''
		};
	} catch (e) {
		error = e instanceof Error ? e.message : 'Failed to load profile';
	} finally {
		loading = false;
	}
}

async function saveProfile() {
	saving = true;
	saveError = '';

	try {
		await api.updateProfile(editForm);
		profile = { ...profile, ...editForm };
		editing = false;
	} catch (e) {
		saveError = e instanceof Error ? e.message : 'Failed to save profile';
	} finally {
		saving = false;
	}
}

function formatDate(dateString: string): string {
	const date = new Date(dateString);
	return date.toLocaleDateString('en-US', { 
		year: 'numeric',
		month: 'short', 
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}
</script>

<svelte:head>
	<title>Profile - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	{#if loading}
		<div class="flex items-center justify-center min-h-[60vh]">
			<div class="text-center">
				<Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin mx-auto mb-4" />
				<p class="text-stone-500 text-sm">Loading profile...</p>
			</div>
		</div>
	{:else if error}
		<div class="max-w-4xl mx-auto px-4 py-8">
			<div class="bg-red-950/30 border border-red-900 rounded-xl p-6 text-center">
				<Icon icon="mdi:alert-circle" class="w-8 h-8 text-red-400 mx-auto mb-3" />
				<p class="text-red-400">{error}</p>
			</div>
		</div>
	{:else if profile}
		<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-12">
			<div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
				<!-- Profile Card -->
				<div class="lg:col-span-1">
					<div class="bg-stone-950/50 border border-stone-800/50 rounded-xl p-8 backdrop-blur-sm">
						{#if editing}
							<form on:submit|preventDefault={saveProfile} class="space-y-5">
								<div>
									<label for="display_name" class="block text-sm font-medium text-stone-300 mb-2">Display Name</label>
									<input
										id="display_name"
										type="text"
										bind:value={editForm.display_name}
										class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
										placeholder="Your display name"
									/>
								</div>

								<div>
									<label for="bio" class="block text-sm font-medium text-stone-300 mb-2">Bio</label>
									<textarea
										id="bio"
										bind:value={editForm.bio}
										rows="3"
										class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition resize-none"
										placeholder="Tell us about yourself..."
									></textarea>
								</div>

								{#if saveError}
									<div class="bg-red-950/30 border border-red-900 rounded px-4 py-3 text-red-400 text-sm">
										{saveError}
									</div>
								{/if}

								<div class="flex space-x-3">
									<button
										type="submit"
										disabled={saving}
										class="flex-1 px-4 py-3 bg-white text-black rounded-lg font-medium hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition"
									>
										{#if saving}
											<Icon icon="mdi:loading" class="w-5 h-5 animate-spin inline-block mr-2" />
											Saving...
										{:else}
											Save Changes
										{/if}
									</button>
									<button
										type="button"
										on:click={() => editing = false}
										class="px-4 py-3 bg-stone-900 text-stone-300 rounded-lg hover:bg-stone-800 transition border border-stone-800"
									>
										Cancel
									</button>
								</div>
							</form>
						{:else}
							<div class="text-center mb-8">
								<div class="w-28 h-28 bg-gradient-to-br from-blue-500 to-purple-600 rounded-2xl mx-auto flex items-center justify-center mb-5 shadow-2xl">
									<Icon icon="mdi:account-circle" class="w-16 h-16 text-white" />
								</div>
								<h1 class="text-2xl font-semibold text-white mb-2 tracking-tight">
									{profile.display_name || profile.username}
								</h1>
								<p class="text-stone-400 text-sm font-mono">@{profile.username}</p>
							</div>

							{#if profile.bio}
								<p class="text-stone-400 text-center mb-8 px-4 text-sm leading-relaxed">{profile.bio}</p>
							{/if}

							<button
								on:click={() => editing = true}
								class="w-full px-4 py-2.5 bg-stone-900/50 text-white rounded-lg hover:bg-stone-800/50 transition border border-stone-700/50 flex items-center justify-center space-x-2 text-sm font-medium"
							>
								<Icon icon="mdi:pencil" class="w-4 h-4" />
								<span>Edit Profile</span>
							</button>

							{#if stats}
								<div class="mt-8 space-y-3">
									<div class="text-center py-5 px-4 bg-black/50 border border-stone-800/50 rounded-xl backdrop-blur-sm">
										<div class="text-4xl font-light text-white mb-1.5 tracking-tight">{stats.total_score || 0}</div>
										<div class="text-xs text-stone-500 uppercase tracking-widest font-medium">Total Points</div>
									</div>
									<div class="grid grid-cols-2 gap-3">
										<div class="text-center py-4 px-3 bg-black/50 border border-stone-800/50 rounded-xl backdrop-blur-sm">
											<div class="text-2xl font-light text-white mb-1 tracking-tight">#{stats.rank || 1}</div>
											<div class="text-xs text-stone-500 uppercase tracking-widest font-medium">Rank</div>
										</div>
										<div class="text-center py-4 px-3 bg-black/50 border border-stone-800/50 rounded-xl backdrop-blur-sm">
											<div class="text-2xl font-light text-white mb-1 tracking-tight">{stats.total_solves || 0}</div>
											<div class="text-xs text-stone-500 uppercase tracking-widest font-medium">Solved</div>
										</div>
									</div>
								</div>
							{/if}
						{/if}
					</div>
				</div>

				<!-- Stats & Activity -->
				<div class="lg:col-span-2 space-y-6">
					<!-- Difficulty Progress -->
					{#if stats?.by_difficulty}
						<div class="bg-stone-950/50 border border-stone-800/50 rounded-xl p-8 backdrop-blur-sm">
							<h2 class="text-lg font-semibold text-white mb-6 flex items-center space-x-2 tracking-tight">
								<Icon icon="mdi:chart-bar" class="w-5 h-5" />
								<span>Progress by Difficulty</span>
							</h2>
							<div class="space-y-6">
								{#each Object.entries(stats.by_difficulty) as [difficulty, data]}
									{@const percentage = data.total > 0 ? (data.solved / data.total) * 100 : 0}
									<div>
										<div class="flex items-center justify-between mb-3">
											<span class="font-medium capitalize text-white text-sm tracking-tight">
												{difficulty}
											</span>
											<span class="text-stone-400 text-xs font-mono">
												{data.solved} / {data.total}
											</span>
										</div>
										<div class="w-full bg-stone-900/50 rounded-full h-2 overflow-hidden">
											<div 
												class="h-full transition-all duration-700 ease-out {getDifficultyColor(difficulty)}"
												style="width: {percentage}%"
											></div>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Category Progress -->
					{#if stats?.by_category && Object.keys(stats.by_category).length > 0}
						<div class="bg-stone-950/50 border border-stone-800/50 rounded-xl p-8 backdrop-blur-sm">
							<h2 class="text-lg font-semibold text-white mb-6 flex items-center space-x-2 tracking-tight">
								<Icon icon="mdi:shape" class="w-5 h-5" />
								<span>Progress by Category</span>
							</h2>
							<div class="grid grid-cols-2 sm:grid-cols-3 gap-4">
								{#each Object.entries(stats.by_category) as [category, data]}
									{@const percentage = data.total > 0 ? (data.solved / data.total) * 100 : 0}
									<div class="p-5 bg-black/50 border border-stone-800/50 rounded-xl backdrop-blur-sm">
										<div class="text-xs text-stone-400 uppercase tracking-wider mb-3 font-medium truncate">{category}</div>
										<div class="text-2xl font-light text-white mb-3 tracking-tight">{data.solved}<span class="text-stone-600 text-lg">/{data.total}</span></div>
										<div class="w-full bg-stone-900/50 rounded-full h-1.5 overflow-hidden">
											<div class="h-full bg-green-500 transition-all duration-700 ease-out" style="width: {percentage}%"></div>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Recent Solves -->
					<div class="bg-stone-950/50 border border-stone-800/50 rounded-xl p-8 backdrop-blur-sm">
						<h2 class="text-lg font-semibold text-white mb-6 flex items-center space-x-2 tracking-tight">
							<Icon icon="mdi:flag-checkered" class="w-5 h-5" />
							<span>Recent Solves</span>
						</h2>

						{#if solves.length === 0}
							<div class="text-center py-16">
								<Icon icon="mdi:flag-off-outline" class="w-14 h-14 text-stone-700 mx-auto mb-4 opacity-50" />
								<p class="text-stone-500 mb-6 text-sm">No flags captured yet</p>
								<a 
									href="/challenges" 
									class="inline-flex items-center space-x-2 px-6 py-2.5 bg-white text-black rounded-lg hover:bg-stone-200 transition text-sm font-medium"
								>
									<Icon icon="mdi:flag" class="w-4 h-4" />
									<span>Browse Challenges</span>
								</a>
							</div>
						{:else}
							<div class="space-y-2">
								{#each solves.slice(0, 10) as solve}
									<a 
										href="/challenges/{solve.challenge_slug}"
										class="flex items-center justify-between p-5 bg-black/50 border border-stone-800/50 rounded-xl hover:border-stone-700/50 transition-all group backdrop-blur-sm"
									>
										<div class="flex items-center space-x-4">
											<div class="w-9 h-9 rounded-lg bg-green-950/30 border border-green-900/50 flex items-center justify-center flex-shrink-0">
												<Icon icon="mdi:check" class="w-5 h-5 text-green-400" />
											</div>
											<div>
												<div class="text-white font-medium group-hover:text-stone-200 transition text-sm tracking-tight">{solve.challenge_name}</div>
												<div class="text-stone-500 text-xs mt-0.5">{solve.flag_name}</div>
											</div>
										</div>
										<div class="text-right">
											<div class="text-green-400 font-medium text-sm">+{solve.points}</div>
											<div class="text-stone-600 text-xs font-mono mt-0.5">{formatDate(solve.solved_at)}</div>
										</div>
									</a>
								{/each}
							</div>
						{/if}
					</div>
				</div>
			</div>
		</div>
	{/if}
</div>
