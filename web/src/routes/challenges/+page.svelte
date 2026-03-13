<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import ChallengeCard from '$lib/components/ChallengeCard.svelte';

	interface Challenge {
		id: string;
		name: string;
		slug: string;
		description?: string;
		difficulty: string;
		category?: string;
		base_points: number;
		total_solves: number;
		total_flags: number;
		user_solves: number;
		is_solved: boolean;
		author_name?: string;
		resource_type?: string;
	}

	let challenges: Challenge[] = [];
	let loading = true;
	let error = '';

	// Filters
	let searchQuery = '';
	let selectedDifficulty = '';
	let selectedCategory = '';
	let showSolved = false;

	$: categories = [...new Set(challenges.map((c) => c.category).filter(Boolean))].sort() as string[];

	$: filteredChallenges = challenges.filter((c) => {
		if (searchQuery && !c.name.toLowerCase().includes(searchQuery.toLowerCase())) return false;
		if (selectedDifficulty && c.difficulty !== selectedDifficulty) return false;
		if (selectedCategory && c.category !== selectedCategory) return false;
		if (showSolved && !c.is_solved) return false;
		return true;
	});

	// Group challenges by category for display when no specific category is selected
	$: groupedChallenges = (() => {
		const isFiltered = searchQuery || selectedDifficulty || selectedCategory || showSolved;
		if (isFiltered || categories.length === 0) {
			return null; // flat display when filtering
		}
		const groups: { category: string; challenges: typeof filteredChallenges }[] = [];
		const uncategorized: typeof filteredChallenges = [];
		for (const cat of categories) {
			const catChallenges = filteredChallenges.filter((c) => c.category === cat);
			if (catChallenges.length > 0) {
				groups.push({ category: cat, challenges: catChallenges });
			}
		}
		for (const c of filteredChallenges) {
			if (!c.category) uncategorized.push(c);
		}
		if (uncategorized.length > 0) {
			groups.push({ category: 'Uncategorized', challenges: uncategorized });
		}
		return groups;
	})();

	onMount(async () => {
		try {
			const response = await api.getChallenges();
			challenges =
				response.challenges?.map((c) => {
					// FIXME: API should be returning correct user_solves and is_solved
					const userSolves = c.user_solves || 0;
					const isSolved = userSolves >= c.total_flags && c.total_flags > 0;
					return { ...c, user_solves: userSolves, is_solved: isSolved };
				}) || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load challenges';
		} finally {
			loading = false;
		}
	});
</script>

<svelte:head>
	<title>Challenges - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<div class="flex flex-col md:flex-row md:items-end md:justify-between mb-8">
			<div class="flex-1">
				<h1 class="text-3xl font-bold text-white">Challenges</h1>
			</div>

			{#if !loading}
				<div class="mt-4 md:mt-0 flex items-center space-x-6 text-sm">
					<div class="flex items-center space-x-2">
						<Icon icon="mdi:flag-outline" class="w-5 h-5 text-stone-500" />
						<span class="text-stone-300">{challenges.length}</span>
						<span class="text-stone-500">challenges</span>
					</div>
					{#if $auth.isAuthenticated}
						<div class="flex items-center space-x-2">
							<Icon icon="mdi:check-circle" class="w-5 h-5 text-green-500" />
							<span class="text-stone-300">{challenges.filter((c) => c.is_solved).length}</span>
							<span class="text-stone-500">solved</span>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- Filters -->
		<div class="bg-stone-950 border border-stone-800 rounded-lg p-6 mb-8">
			<div class="grid grid-cols-1 md:grid-cols-4 gap-4">
				<div class="relative">
					<Icon
						icon="mdi:magnify"
						class="absolute left-3 top-1/2 -translate-y-1/2 w-5 h-5 text-stone-500"
					/>
					<input
						type="text"
						bind:value={searchQuery}
						placeholder="Search challenges..."
						class="w-full pl-10 pr-4 py-3 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					/>
				</div>

				<select
					bind:value={selectedDifficulty}
					class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
				>
					<option value="">All Difficulties</option>
					<option value="easy">Easy</option>
					<option value="medium">Medium</option>
					<option value="hard">Hard</option>
					<option value="insane">Insane</option>
				</select>

				<select
					bind:value={selectedCategory}
					class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
				>
					<option value="">All Categories</option>
					{#each categories as category}
						<option value={category}>{category}</option>
					{/each}
				</select>

				{#if $auth.isAuthenticated}
					<label
						class="flex items-center space-x-3 px-4 py-3 bg-black border border-stone-700 rounded-lg cursor-pointer hover:border-stone-600 transition"
					>
						<input
							type="checkbox"
							bind:checked={showSolved}
							class="w-4 h-4 rounded border-stone-600 bg-stone-900 text-white focus:ring-0 focus:ring-offset-0"
						/>
						<span class="text-stone-300 text-sm">Show Solved Only</span>
					</label>
				{/if}
			</div>
		</div>

		{#if loading}
			<div class="flex items-center justify-center py-32">
				<div class="text-center">
					<Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin mx-auto mb-4" />
					<p class="text-stone-500">Loading challenges...</p>
				</div>
			</div>
		{:else if error}
			<div class="bg-red-950/30 border border-red-900 rounded-lg p-6 text-center">
				<Icon icon="mdi:alert-circle" class="w-8 h-8 text-red-400 mx-auto mb-3" />
				<p class="text-red-400">{error}</p>
			</div>
		{:else if filteredChallenges.length === 0}
			<div class="text-center py-32">
				<Icon icon="mdi:flag-off-outline" class="w-16 h-16 text-stone-700 mx-auto mb-4" />
				<h3 class="text-xl font-semibold text-white mb-2">No Challenges Found</h3>
				<p class="text-stone-500">Try adjusting your filters</p>
			</div>
		{:else if groupedChallenges}
			<!-- Category-grouped display -->
			<div class="space-y-10">
				{#each groupedChallenges as group}
					<div>
						<div class="flex items-center gap-3 mb-4">
							<h2 class="text-lg font-semibold text-white">{group.category}</h2>
							<span class="text-xs text-stone-500 bg-stone-900 border border-stone-800 rounded-full px-2.5 py-0.5">{group.challenges.length}</span>
							<div class="flex-1 h-px bg-stone-800"></div>
						</div>
						<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
							{#each group.challenges as challenge}
								<ChallengeCard {challenge} />
							{/each}
						</div>
					</div>
				{/each}
			</div>
		{:else}
			<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
				{#each filteredChallenges as challenge}
					<ChallengeCard {challenge} />
				{/each}
			</div>
		{/if}
	</div>
</div>
