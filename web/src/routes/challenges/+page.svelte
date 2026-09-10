<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import { categoryColor } from '$lib/rank';
	import ChallengeTile from '$lib/components/ChallengeTile.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

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

	// Icons only — the accent color always comes from the muted categoryColor palette.
	const categoryIcons: Record<string, string> = {
		'Web Exploitation': 'mdi:web',
		'Binary Exploitation': 'mdi:memory',
		'Reverse Engineering': 'mdi:cog-outline',
		Cryptography: 'mdi:key-variant',
		Forensics: 'mdi:fingerprint',
		OSINT: 'mdi:earth',
		Misc: 'mdi:shape-outline'
	};

	function catIcon(name: string): string {
		return categoryIcons[name] ?? 'mdi:flag-outline';
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

<div class="min-h-screen bg-stone-950">
	<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<PageHeader
			title="Challenges"
			subtitle={loading ? 'Loading…' : `${categories.length} categories · pick a target`}
		>
			<svelte:fragment slot="actions">
				{#if !loading}
					<div class="flex items-center gap-5 text-sm">
						<div class="flex items-center gap-2">
							<Icon icon="mdi:flag-outline" class="w-4 h-4 text-stone-500" />
							<span class="text-stone-200 tabular-nums">{challenges.length}</span>
							<span class="text-stone-500 text-xs uppercase tracking-wide">challenges</span>
						</div>
						{#if $auth.isAuthenticated}
							<div class="flex items-center gap-2">
								<Icon icon="mdi:check-circle" class="w-4 h-4 text-up" />
								<span class="text-stone-200 tabular-nums">{solvedCount}</span>
								<span class="text-stone-500 text-xs uppercase tracking-wide">solved</span>
							</div>
						{/if}
					</div>
				{/if}
			</svelte:fragment>
		</PageHeader>

		<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4 mb-8">
			<div class="grid grid-cols-1 md:grid-cols-4 gap-3">
				<div class="relative md:col-span-2">
					<Icon icon="mdi:magnify" class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-stone-500" />
					<input
						type="text"
						bind:value={searchQuery}
						placeholder="Search challenges…"
						class="w-full pl-9 pr-9 py-2 bg-stone-950 border border-stone-800 rounded-md text-sm text-stone-100 placeholder-stone-600 focus:outline-none focus:border-stone-600 focus:ring-1 focus:ring-stone-600 transition-colors"
					/>
					{#if searchQuery}
						<button
							on:click={() => (searchQuery = '')}
							class="absolute right-2 top-1/2 -translate-y-1/2 text-stone-600 hover:text-stone-300 transition-colors"
							aria-label="Clear search"
						>
							<Icon icon="mdi:close-circle" class="w-4 h-4" />
						</button>
					{/if}
				</div>

				<select
					bind:value={selectedDifficulty}
					class="w-full px-3 py-2 bg-stone-950 border border-stone-800 rounded-md text-sm text-stone-100 focus:outline-none focus:border-stone-600 focus:ring-1 focus:ring-stone-600 transition-colors"
				>
					<option value="">All difficulties</option>
					<option value="easy">Easy</option>
					<option value="medium">Medium</option>
					<option value="hard">Hard</option>
					<option value="insane">Insane</option>
				</select>

				<select
					bind:value={selectedCategory}
					class="w-full px-3 py-2 bg-stone-950 border border-stone-800 rounded-md text-sm text-stone-100 focus:outline-none focus:border-stone-600 focus:ring-1 focus:ring-stone-600 transition-colors"
				>
					<option value="">All categories</option>
					{#each categories as category}
						<option value={category}>{category}</option>
					{/each}
				</select>
			</div>

			{#if $auth.isAuthenticated || hasFilters}
				<div class="flex items-center justify-between gap-4 mt-3">
					{#if $auth.isAuthenticated}
						<label class="inline-flex items-center gap-2 cursor-pointer select-none">
							<input
								type="checkbox"
								bind:checked={showSolved}
								class="w-3.5 h-3.5 rounded-sm border-stone-700 bg-stone-950 accent-amber-600 focus:ring-0 focus:ring-offset-0"
							/>
							<span class="text-stone-400 text-xs uppercase tracking-wide">Solved only</span>
						</label>
					{:else}
						<span></span>
					{/if}
					{#if hasFilters}
						<button on:click={resetFilters} class="text-xs text-stone-500 hover:text-stone-300 transition-colors inline-flex items-center gap-1">
							<Icon icon="mdi:filter-remove-outline" class="w-4 h-4" />
							Reset filters
						</button>
					{/if}
				</div>
			{/if}
		</div>

		{#if loading}
			<div class="space-y-10">
				{#each [0, 1] as s (s)}
					<section>
						<div class="flex items-center gap-3 mb-4">
							<span class="w-2 h-2 rounded-full bg-stone-800"></span>
							<span class="h-3 w-40 rounded bg-stone-900/60 animate-pulse"></span>
							<div class="flex-1 h-px bg-stone-800/60"></div>
						</div>
						<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
							{#each [0, 1, 2] as k (k)}
								<div class="h-32 rounded-lg border border-stone-800 bg-stone-900/40 animate-pulse"></div>
							{/each}
						</div>
					</section>
				{/each}
			</div>
		{:else if error}
			<div class="rounded-lg border border-down/30 bg-down/10 px-4 py-3 flex items-center gap-2.5">
				<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 text-down shrink-0" />
				<p class="text-down text-sm">{error}</p>
			</div>
		{:else if filteredChallenges.length === 0}
			<div class="rounded-lg border border-stone-800 bg-stone-900/40 py-4">
				<EmptyState icon="mdi:flag-off-outline" text="No challenges match the current filters.">
					{#if hasFilters}
						<button on:click={resetFilters} class="mt-3 text-xs text-amber-500 hover:text-amber-400 transition-colors">Reset filters</button>
					{/if}
				</EmptyState>
			</div>
		{:else}
			<div class="space-y-10">
				{#each groups as group (group.category)}
					{@const color = categoryColor(group.category)}
					<section>
						<div class="flex items-center gap-2.5 mb-4">
							<Icon icon={catIcon(group.category)} class="w-4 h-4 shrink-0" style="color:{color}" />
							<h2 class="text-[0.95rem] font-semibold text-stone-200 leading-none">{group.category}</h2>
							<span class="text-[0.68rem] tabular-nums rounded-full px-2 py-0.5 border border-stone-800 bg-stone-900/40 {group.solved === group.challenges.length && $auth.isAuthenticated ? 'text-up' : 'text-stone-400'}">
								{#if $auth.isAuthenticated}{group.solved}/{group.challenges.length}{:else}{group.challenges.length}{/if}
							</span>
							<div class="flex-1 h-px bg-stone-800/60"></div>
						</div>
						<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
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
