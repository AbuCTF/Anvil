<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import ChallengeTile from '$lib/components/ChallengeTile.svelte';

	interface Challenge {
		id: string;
		name: string;
		slug: string;
		description?: string;
		difficulty: string;
		category?: string;
		category_id?: string;
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

	let searchQuery = '';
	let selectedDifficulty = '';
	let selectedCategory = '';
	let showSolved = false;

	const categoryMeta: Record<string, { icon: string; hue: number }> = {
		'Web Exploitation': { icon: 'mdi:web', hue: 205 },
		'Binary Exploitation': { icon: 'mdi:memory', hue: 0 },
		'Reverse Engineering': { icon: 'mdi:cog-outline', hue: 265 },
		Cryptography: { icon: 'mdi:key-variant', hue: 45 },
		Forensics: { icon: 'mdi:fingerprint', hue: 160 },
		OSINT: { icon: 'mdi:earth', hue: 190 },
		Misc: { icon: 'mdi:shape-outline', hue: 315 }
	};

	function catHue(name: string): number {
		let h = 0;
		for (let i = 0; i < name.length; i++) h = (Math.imul(h, 31) + name.charCodeAt(i)) >>> 0;
		return h % 360;
	}

	function catInfo(name: string) {
		return categoryMeta[name] ?? { icon: 'mdi:flag-outline', hue: catHue(name) };
	}

	$: categories = [...new Set(challenges.map((c) => c.category).filter(Boolean))].sort() as string[];

	$: hasFilters = !!(searchQuery || selectedDifficulty || selectedCategory || showSolved);

	$: filteredChallenges = challenges.filter((c) => {
		if (searchQuery && !c.name.toLowerCase().includes(searchQuery.toLowerCase())) return false;
		if (selectedDifficulty && c.difficulty !== selectedDifficulty) return false;
		if (selectedCategory && c.category !== selectedCategory) return false;
		if (showSolved && !c.is_solved) return false;
		return true;
	});

	$: groups = (() => {
		const known = [...new Set(filteredChallenges.map((c) => c.category).filter(Boolean))].sort() as string[];
		const out: { category: string; challenges: Challenge[]; solved: number }[] = [];
		for (const cat of known) {
			const list = filteredChallenges.filter((c) => c.category === cat);
			out.push({ category: cat, challenges: list, solved: list.filter((c) => c.is_solved).length });
		}
		const uncategorized = filteredChallenges.filter((c) => !c.category);
		if (uncategorized.length > 0) {
			out.push({
				category: 'Uncategorized',
				challenges: uncategorized,
				solved: uncategorized.filter((c) => c.is_solved).length
			});
		}
		return out;
	})();

	$: solvedCount = challenges.filter((c) => c.is_solved).length;

	function resetFilters() {
		searchQuery = '';
		selectedDifficulty = '';
		selectedCategory = '';
		showSolved = false;
	}

	onMount(async () => {
		try {
			const response = await api.getChallenges();
			challenges =
				response.challenges?.map((c) => {
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
		<div class="flex flex-col md:flex-row md:items-end md:justify-between gap-4 mb-8">
			<div>
				<h1 class="text-3xl font-bold text-white">Challenges</h1>
				<p class="text-stone-500 text-sm mt-1">{categories.length} categories · pick a target</p>
			</div>

			{#if !loading}
				<div class="flex items-center gap-6 text-sm">
					<div class="flex items-center gap-2">
						<Icon icon="mdi:flag-outline" class="w-5 h-5 text-stone-500" />
						<span class="text-stone-300 tabular-nums">{challenges.length}</span>
						<span class="text-stone-500">challenges</span>
					</div>
					{#if $auth.isAuthenticated}
						<div class="flex items-center gap-2">
							<Icon icon="mdi:check-circle" class="w-5 h-5 text-green-500" />
							<span class="text-stone-300 tabular-nums">{solvedCount}</span>
							<span class="text-stone-500">solved</span>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<div class="bg-stone-900/50 border border-stone-800 rounded-xl p-4 sm:p-5 mb-8">
			<div class="grid grid-cols-1 md:grid-cols-4 gap-3">
				<div class="relative md:col-span-2">
					<Icon icon="mdi:magnify" class="absolute left-3 top-1/2 -translate-y-1/2 w-5 h-5 text-stone-500" />
					<input
						type="text"
						bind:value={searchQuery}
						placeholder="Search challenges..."
						class="w-full pl-10 pr-9 py-2.5 bg-black border border-stone-700 rounded-lg text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
					/>
					{#if searchQuery}
						<button
							on:click={() => (searchQuery = '')}
							class="absolute right-2 top-1/2 -translate-y-1/2 text-stone-600 hover:text-stone-300 transition"
							aria-label="Clear search"
						>
							<Icon icon="mdi:close-circle" class="w-4 h-4" />
						</button>
					{/if}
				</div>

				<select
					bind:value={selectedDifficulty}
					class="w-full px-4 py-2.5 bg-black border border-stone-700 rounded-lg text-white focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
				>
					<option value="">All Difficulties</option>
					<option value="easy">Easy</option>
					<option value="medium">Medium</option>
					<option value="hard">Hard</option>
					<option value="insane">Insane</option>
				</select>

				<select
					bind:value={selectedCategory}
					class="w-full px-4 py-2.5 bg-black border border-stone-700 rounded-lg text-white focus:outline-none focus:border-stone-500 focus:ring-1 focus:ring-stone-500 transition"
				>
					<option value="">All Categories</option>
					{#each categories as category}
						<option value={category}>{category}</option>
					{/each}
				</select>
			</div>

			{#if $auth.isAuthenticated || hasFilters}
				<div class="flex items-center justify-between gap-4 mt-3">
					{#if $auth.isAuthenticated}
						<label class="inline-flex items-center gap-2.5 cursor-pointer select-none">
							<input
								type="checkbox"
								bind:checked={showSolved}
								class="w-4 h-4 rounded border-stone-600 bg-stone-900 text-green-500 focus:ring-0 focus:ring-offset-0"
							/>
							<span class="text-stone-400 text-sm">Solved only</span>
						</label>
					{:else}
						<span></span>
					{/if}
					{#if hasFilters}
						<button on:click={resetFilters} class="text-xs text-stone-500 hover:text-stone-300 transition inline-flex items-center gap-1">
							<Icon icon="mdi:filter-remove-outline" class="w-4 h-4" />
							Reset filters
						</button>
					{/if}
				</div>
			{/if}
		</div>

		{#if loading}
			<div class="flex items-center justify-center py-32">
				<div class="text-center">
					<Icon icon="mdi:loading" class="w-8 h-8 text-amber-500 animate-spin mx-auto mb-4" />
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
				<p class="text-stone-500 mb-6">Try adjusting your filters</p>
				{#if hasFilters}
					<button on:click={resetFilters} class="text-sm text-amber-500 hover:text-amber-400 transition">Reset filters</button>
				{/if}
			</div>
		{:else}
			<div class="space-y-10">
				{#each groups as group (group.category)}
					{@const info = catInfo(group.category)}
					<section>
						<div class="flex items-center gap-3 mb-4">
							<span
								class="inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0"
								style="background: hsl({info.hue} 45% 15%); color: hsl({info.hue} 75% 66%);"
							>
								<Icon icon={info.icon} class="w-5 h-5" />
							</span>
							<h2 class="text-lg font-semibold text-white">{group.category}</h2>
							<span
								class="text-xs tabular-nums rounded-full px-2.5 py-0.5 border"
								class:text-green-400={group.solved === group.challenges.length}
								style="color: hsl({info.hue} 60% 70%); background: hsl({info.hue} 45% 12% / 0.6); border-color: hsl({info.hue} 45% 24%);"
							>
								{#if $auth.isAuthenticated}{group.solved}/{group.challenges.length}{:else}{group.challenges.length}{/if}
							</span>
							<div
								class="flex-1 h-px"
								style="background: linear-gradient(to right, hsl({info.hue} 45% 28%), transparent);"
							></div>
						</div>
						<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
							{#each group.challenges as challenge (challenge.id)}
								<ChallengeTile {challenge} />
							{/each}
						</div>
					</section>
				{/each}
			</div>
		{/if}
	</div>
</div>
