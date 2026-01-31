<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

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
	}

	let challenges: Challenge[] = [];
	let loading = true;
	let error = '';

	// Filters
	let searchQuery = '';
	let selectedDifficulty = '';
	let selectedCategory = '';
	let showSolved = true;

	$: categories = [...new Set(challenges.map(c => c.category).filter(Boolean))] as string[];

	$: filteredChallenges = challenges.filter(c => {
		if (searchQuery && !c.name.toLowerCase().includes(searchQuery.toLowerCase())) return false;
		if (selectedDifficulty && c.difficulty !== selectedDifficulty) return false;
		if (selectedCategory && c.category !== selectedCategory) return false;
		if (!showSolved && c.is_solved) return false;
		return true;
	});

	function getDifficultyColor(difficulty: string): string {
		switch(difficulty.toLowerCase()) {
			case 'easy': return 'text-green-400 border-green-900 bg-green-950/30';
			case 'medium': return 'text-yellow-400 border-yellow-900 bg-yellow-950/30';
			case 'hard': return 'text-red-400 border-red-900 bg-red-950/30';
			case 'insane': return 'text-purple-400 border-purple-900 bg-purple-950/30';
			default: return 'text-stone-400 border-stone-800';
		}
	}

	onMount(async () => {
		try {
			const response = await api.getChallenges();
			challenges = response.challenges || [];
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
		<div class="flex flex-col md:flex-row md:items-center md:justify-between mb-8">
			<div>
				<h1 class="text-3xl font-bold text-white">Challenges</h1>
				<p class="mt-2 text-stone-400">Boot-to-root machines and security challenges</p>
			</div>

			{#if !loading}
				<div class="mt-4 md:mt-0 flex items-center space-x-6 text-sm">
					<div class="flex items-center space-x-2">
						<Icon icon="mdi:flag-outline" class="w-5 h-5 text-stone-500" />
						<span class="text-stone-300">{filteredChallenges.length}</span>
						<span class="text-stone-500">challenges</span>
					</div>
					{#if $auth.isAuthenticated}
						<div class="flex items-center space-x-2">
							<Icon icon="mdi:check-circle" class="w-5 h-5 text-green-500" />
							<span class="text-stone-300">{challenges.filter(c => c.is_solved).length}</span>
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
					<Icon icon="mdi:magnify" class="absolute left-3 top-1/2 -translate-y-1/2 w-5 h-5 text-stone-500" />
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
					<label class="flex items-center space-x-3 px-4 py-3 bg-black border border-stone-700 rounded-lg cursor-pointer hover:border-stone-600 transition">
						<input
							type="checkbox"
							bind:checked={showSolved}
							class="w-4 h-4 rounded border-stone-600 bg-stone-900 text-white focus:ring-0 focus:ring-offset-0"
						/>
						<span class="text-stone-300 text-sm">Show Solved</span>
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
		{:else}
			<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
				{#each filteredChallenges as challenge}
					<a
						href="/challenges/{challenge.slug}"
						class="group bg-stone-950 border border-stone-800 rounded-lg overflow-hidden hover:border-stone-700 transition-all duration-200 {challenge.is_solved ? 'ring-1 ring-green-900/30' : ''}"
					>
						<div class="p-6">
							<div class="flex items-start justify-between mb-4">
								<h3 class="text-lg font-semibold text-white group-hover:text-stone-200 transition flex-1">
									{challenge.name}
								</h3>
								{#if challenge.is_solved}
									<Icon icon="mdi:check-circle" class="w-5 h-5 text-green-500 flex-shrink-0 ml-2" />
								{/if}
							</div>

							{#if challenge.description}
								<p class="text-sm text-stone-400 line-clamp-2 mb-4">
									{challenge.description}
								</p>
							{/if}

							<div class="flex flex-wrap items-center gap-2 mb-4">
								<span class="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium border {getDifficultyColor(challenge.difficulty)}">
									{challenge.difficulty}
								</span>

								{#if challenge.category}
									<span class="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-stone-900 text-stone-400 border border-stone-800">
										{challenge.category}
									</span>
								{/if}
							</div>

							<div class="flex items-center justify-between text-sm text-stone-400 mb-4">
								<div class="flex items-center space-x-4">
									<div class="flex items-center space-x-1.5">
										<Icon icon="mdi:star-outline" class="w-4 h-4" />
										<span>{challenge.base_points}</span>
									</div>
									<div class="flex items-center space-x-1.5">
										<Icon icon="mdi:flag-outline" class="w-4 h-4" />
										<span>{challenge.total_flags}</span>
									</div>
								</div>

								<div class="flex items-center space-x-1.5 text-stone-500">
									<Icon icon="mdi:account-group" class="w-4 h-4" />
									<span>{challenge.total_solves}</span>
								</div>
							</div>

							{#if $auth.isAuthenticated && challenge.total_flags > 0}
								<div class="pt-4 border-t border-stone-800">
									<div class="flex items-center justify-between text-xs mb-2">
										<span class="text-stone-500">Progress</span>
										<span class="text-stone-400">{challenge.user_solves || 0}/{challenge.total_flags}</span>
									</div>
									<div class="w-full bg-stone-900 rounded-full h-2 overflow-hidden">
										<div
											class="h-full bg-gradient-to-r from-green-500 to-emerald-500 transition-all duration-500"
											style="width: {((challenge.user_solves || 0) / challenge.total_flags) * 100}%"
										></div>
									</div>
								</div>
							{/if}
						</div>

						{#if challenge.author_name}
							<div class="px-6 py-3 bg-black border-t border-stone-800">
								<p class="text-xs text-stone-500">
									by <span class="text-stone-400">{challenge.author_name}</span>
								</p>
							</div>
						{/if}
					</a>
				{/each}
			</div>
		{/if}
	</div>
</div>
