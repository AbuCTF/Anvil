<script lang="ts">
import { onMount } from 'svelte';
import Icon from '@iconify/svelte';
import { api } from '$api';

interface UserStats {
	total_score: number;
	total_solves: number;
	total_challenges_solved: number;
	rank: number;
	solves_by_difficulty: Record<string, number>;
	solves_by_category: Record<string, number>;
}

interface Progress {
	solved: number;
	total: number;
}

interface Solve {
	challenge_name: string;
	challenge_slug: string;
	flag_name: string;
	points: number;
	solved_at: number;
}

let profile: any = null;
let stats: UserStats | null = null;
let solves: Solve[] = [];
let byDifficulty: Record<string, Progress> = {};
let byCategory: Record<string, Progress> = {};
let loading = true;
let error = '';

let editing = false;
let editForm = {
	display_name: '',
	bio: ''
};
let saving = false;
let saveError = '';

onMount(async () => {
	await loadProfile();
});

async function loadProfile() {
	loading = true;
	error = '';
	try {
		const [profileRes, statsRes, solvesRes, challengesRes] = await Promise.all([
			api.getProfile(),
			api.getUserStats(),
			api.getUserSolves(),
			api.getChallenges()
		]);

		profile = profileRes;
		stats = statsRes;
		solves = solvesRes.solves || [];

		const difficultyTotals: Record<string, number> = {};
		const categoryTotals: Record<string, number> = {};
		for (const challenge of challengesRes.challenges || []) {
			const difficulty = challenge.difficulty || 'Unrated';
			const category = challenge.category || 'Uncategorized';
			difficultyTotals[difficulty] = (difficultyTotals[difficulty] || 0) + 1;
			categoryTotals[category] = (categoryTotals[category] || 0) + 1;
		}
		byDifficulty = Object.fromEntries(
			Object.entries(difficultyTotals).map(([name, total]) => [
				name,
				{ solved: statsRes.solves_by_difficulty?.[name] || 0, total }
			])
		);
		byCategory = Object.fromEntries(
			Object.entries(categoryTotals).map(([name, total]) => [
				name,
				{ solved: statsRes.solves_by_category?.[name] || 0, total }
			])
		);

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
		const saved = {
			display_name: editForm.display_name.trim(),
			bio: editForm.bio.trim()
		};
		profile = { ...profile, ...saved };
		editForm = saved;
		editing = false;
	} catch (e) {
		saveError = e instanceof Error ? e.message : 'Failed to save profile';
	} finally {
		saving = false;
	}
}

function formatDate(timestamp: number): string {
	const date = new Date(timestamp * 1000);
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

<div class="min-h-screen bg-stone-950">
	{#if loading}
		<div class="flex items-center justify-center min-h-[60vh]">
			<div class="text-center">
				<Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin mx-auto mb-4" />
				<p class="text-stone-500 text-sm">Loading profile...</p>
			</div>
		</div>
	{:else if error}
		<div class="max-w-4xl mx-auto px-4 py-8">
			<div class="bg-down/10 border border-down/30 rounded-lg p-6 text-center">
				<Icon icon="mdi:alert-circle" class="w-8 h-8 text-down mx-auto mb-3" />
				<p class="text-down">{error}</p>
				<button
					on:click={loadProfile}
					class="mt-4 px-4 py-2 rounded-md border border-down/30 text-sm text-stone-200 hover:bg-down/10 transition-colors"
				>
					Try again
				</button>
			</div>
		</div>
	{:else if profile}
		<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-12">
			<div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
				<!-- Profile Card -->
				<div class="lg:col-span-1">
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-6">
						{#if editing}
							<form on:submit|preventDefault={saveProfile} class="space-y-5">
								<div>
									<label for="display_name" class="block text-sm font-medium text-stone-300 mb-2">Display Name</label>
									<input
									id="display_name"
									type="text"
									bind:value={editForm.display_name}
									maxlength="100"
									class="w-full px-3 py-2.5 bg-stone-950 border border-stone-700 rounded-md text-stone-50 placeholder-stone-500 focus:outline-none focus:border-stone-500 transition"
										placeholder="Your display name"
									/>
								</div>

								<div>
									<label for="bio" class="block text-sm font-medium text-stone-300 mb-2">Bio</label>
									<textarea
									id="bio"
									bind:value={editForm.bio}
									maxlength="2000"
									rows="3"
									class="w-full px-3 py-2.5 bg-stone-950 border border-stone-700 rounded-md text-stone-50 placeholder-stone-500 focus:outline-none focus:border-stone-500 transition resize-none"
										placeholder="Tell us about yourself..."
									></textarea>
								</div>

								{#if saveError}
									<div class="bg-down/10 border border-down/30 rounded-md px-4 py-3 text-down text-sm">
										{saveError}
									</div>
								{/if}

								<div class="flex space-x-3">
									<button
										type="submit"
										disabled={saving}
										class="flex-1 inline-flex items-center justify-center px-4 py-2.5 bg-stone-50 text-stone-950 rounded-md leading-none font-medium hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition"
									>
										{#if saving}
											<Icon icon="mdi:loading" class="w-4 h-4 shrink-0 animate-spin mr-2" />
											Saving...
										{:else}
											Save Changes
										{/if}
									</button>
									<button
										type="button"
										on:click={() => editing = false}
										class="px-4 py-2.5 bg-stone-900 text-stone-300 rounded-md hover:bg-stone-800 transition border border-stone-800"
									>
										Cancel
									</button>
								</div>
							</form>
						{:else}
							<div class="text-center mb-6">
								<div class="w-20 h-20 bg-stone-900/60 border border-stone-800 rounded-lg mx-auto flex items-center justify-center mb-4">
									<Icon icon="mdi:account-circle" class="w-11 h-11 text-stone-500" />
								</div>
								<h1 class="text-2xl font-semibold text-stone-50 mb-2 tracking-tight">
									{profile.display_name || profile.username}
								</h1>
								<p class="text-stone-400 text-sm font-mono">@{profile.username}</p>
							</div>

							{#if profile.bio}
								<p class="text-stone-400 text-center mb-8 px-4 text-sm leading-relaxed">{profile.bio}</p>
							{/if}

							<button
								on:click={() => editing = true}
								class="w-full px-4 py-2.5 bg-stone-900/50 text-stone-50 rounded-md hover:bg-stone-800/50 transition border border-stone-700/50 flex items-center justify-center space-x-2 text-sm leading-none font-medium"
							>
								<Icon icon="mdi:pencil" class="w-3.5 h-3.5 shrink-0" />
								<span>Edit Profile</span>
							</button>

							{#if stats}
								<div class="mt-8 space-y-3">
									<div class="text-center py-5 px-4 bg-stone-950/50 border border-stone-800 rounded-lg">
										<div class="text-4xl font-light text-stone-50 mb-1.5 tracking-tight tabular-nums">{stats.total_score || 0}</div>
										<div class="metadata-label text-stone-500">Total Points</div>
									</div>
									<div class="grid grid-cols-2 gap-3">
										<div class="text-center py-4 px-3 bg-stone-950/50 border border-stone-800 rounded-lg">
											<div class="text-2xl font-light text-stone-50 mb-1 tracking-tight tabular-nums">{stats.rank ? `#${stats.rank}` : '—'}</div>
											<div class="metadata-label text-stone-500">Rank</div>
										</div>
										<div class="text-center py-4 px-3 bg-stone-950/50 border border-stone-800 rounded-lg">
											<div class="text-2xl font-light text-stone-50 mb-1 tracking-tight tabular-nums">{stats.total_challenges_solved || 0}</div>
											<div class="metadata-label text-stone-500">Solved</div>
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
					{#if Object.keys(byDifficulty).length > 0}
						<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-6">
							<h2 class="text-lg leading-none font-semibold text-stone-50 mb-6 flex items-center space-x-2 tracking-tight">
								<Icon icon="mdi:chart-bar" class="w-[18px] h-[18px] shrink-0" />
								<span>Progress by Difficulty</span>
							</h2>
							<div class="space-y-6">
								{#each Object.entries(byDifficulty) as [difficulty, data]}
									{@const percentage = data.total > 0 ? (data.solved / data.total) * 100 : 0}
									<div>
										<div class="flex items-center justify-between mb-3">
											<span class="font-medium capitalize text-stone-50 text-sm tracking-tight">
												{difficulty}
											</span>
											<span class="text-stone-400 text-xs font-mono">
												{data.solved} / {data.total}
											</span>
										</div>
										<div class="w-full bg-stone-900/50 rounded-full h-2 overflow-hidden">
											<div 
												class="h-full bg-stone-500 transition-all duration-700 ease-out"
												style="width: {percentage}%"
											></div>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Category Progress -->
					{#if Object.keys(byCategory).length > 0}
						<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-6">
							<h2 class="text-lg leading-none font-semibold text-stone-50 mb-6 flex items-center space-x-2 tracking-tight">
								<Icon icon="mdi:shape" class="w-[18px] h-[18px] shrink-0" />
								<span>Progress by Category</span>
							</h2>
							<div class="grid grid-cols-2 sm:grid-cols-3 gap-4">
								{#each Object.entries(byCategory) as [category, data]}
									{@const percentage = data.total > 0 ? (data.solved / data.total) * 100 : 0}
									<div class="p-5 bg-stone-950/50 border border-stone-800 rounded-lg">
										<div class="metadata-label text-stone-400 mb-3 truncate">{category}</div>
										<div class="text-2xl font-light text-stone-50 mb-3 tracking-tight">{data.solved}<span class="text-stone-600 text-lg">/{data.total}</span></div>
										<div class="w-full bg-stone-900/50 rounded-full h-1.5 overflow-hidden">
											<div class="h-full bg-stone-500 transition-all duration-700 ease-out" style="width: {percentage}%"></div>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Recent Solves -->
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-6">
						<h2 class="text-lg leading-none font-semibold text-stone-50 mb-6 flex items-center space-x-2 tracking-tight">
							<Icon icon="mdi:flag-checkered" class="w-[18px] h-[18px] shrink-0" />
							<span>Recent Solves</span>
						</h2>

						{#if solves.length === 0}
							<div class="text-center py-16">
								<Icon icon="mdi:flag-off-outline" class="w-14 h-14 text-stone-700 mx-auto mb-4 opacity-50" />
								<p class="text-stone-500 mb-6 text-sm">No flags captured yet</p>
								<a 
									href="/challenges" 
									class="inline-flex items-center space-x-2 px-6 py-2.5 bg-stone-50 text-stone-950 rounded-md hover:bg-stone-200 transition text-sm leading-none font-medium"
								>
									<Icon icon="mdi:flag" class="w-3.5 h-3.5 shrink-0" />
									<span>Browse Challenges</span>
								</a>
							</div>
						{:else}
							<div class="space-y-2">
								{#each solves.slice(0, 10) as solve}
									<a 
										href="/challenges/{solve.challenge_slug}"
										class="flex items-center justify-between p-5 bg-stone-950/50 border border-stone-800 rounded-md hover:border-stone-700 transition-colors group"
									>
										<div class="flex items-center space-x-4">
											<div class="w-9 h-9 rounded-md bg-up/10 border border-up/30 flex items-center justify-center flex-shrink-0">
												<Icon icon="mdi:check" class="w-5 h-5 text-up" />
											</div>
											<div>
												<div class="text-stone-50 font-medium group-hover:text-stone-200 transition text-sm tracking-tight">{solve.challenge_name}</div>
												<div class="text-stone-500 text-xs mt-0.5">{solve.flag_name}</div>
											</div>
										</div>
										<div class="text-right">
											<div class="text-up font-medium text-sm tabular-nums">+{solve.points}</div>
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
