<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import { categoryColor } from '$lib/rank';
	import ChallengeTile from '$lib/components/ChallengeTile.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';

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
	const categoryIcons: Record<string, { icon: string; size: number }> = {
		b2r: { icon: 'mdi:server-security', size: 13.75 },
		web3: { icon: 'mdi:ethereum', size: 15.5 },
		'web exploitation': { icon: 'mdi:web', size: 13.75 },
		'binary exploitation': { icon: 'mdi:memory', size: 15.25 },
		'reverse engineering': { icon: 'mdi:cog-outline', size: 13.75 },
		cryptography: { icon: 'mdi:key-variant', size: 13.75 },
		forensics: { icon: 'mdi:fingerprint', size: 13.75 },
		osint: { icon: 'mdi:earth', size: 13.75 },
		misc: { icon: 'mdi:shape-outline', size: 13.75 }
	};

	function catIcon(name: string) {
		return categoryIcons[name.trim().toLowerCase()] ?? { icon: 'mdi:flag-outline', size: 15.25 };
	}

	$: categories = [...new Set(challenges.map((c) => c.category).filter(Boolean))].sort() as string[];

	$: hasFilters = !!(searchQuery || selectedDifficulty || selectedCategory || showSolved);
	$: solvedOnlyEmpty = showSolved && !searchQuery && !selectedDifficulty && !selectedCategory;

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
	<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-8">
		<PageHeader
			title="Challenges"
			subtitle={loading ? 'Loading…' : `${categories.length} categories · pick a target`}
		>
			<svelte:fragment slot="actions">
				{#if !loading}
					<div class="flex items-center gap-5 text-sm leading-none">
						<div class="inline-flex items-center gap-1.5 whitespace-nowrap">
							<OpticalIcon
								icon="mdi:flag-outline"
								size={13.5}
								box={14}
								className="text-stone-500"
							/>
							<span class="optical-label inline-flex items-baseline gap-1.5">
								<span class="font-semibold text-stone-200 tabular-nums">{challenges.length}</span>
								<span class="metadata-label text-stone-500">Challenges</span>
							</span>
						</div>
						{#if $auth.isAuthenticated}
							<div class="inline-flex items-center gap-1.5 whitespace-nowrap">
								<OpticalIcon
									icon="mdi:check-circle"
									size={13}
									box={14}
									className="text-up"
								/>
								<span class="optical-label inline-flex items-baseline gap-1.5">
									<span class="font-semibold text-stone-200 tabular-nums">{solvedCount}</span>
									<span class="metadata-label text-stone-500">Solved</span>
								</span>
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
							<span class="relative top-px metadata-label text-stone-400">Solved only</span>
						</label>
					{:else}
						<span></span>
					{/if}
					{#if hasFilters && filteredChallenges.length > 0}
						<button on:click={resetFilters} class="text-xs leading-none text-stone-500 hover:text-stone-300 transition-colors inline-flex items-center gap-1">
							<OpticalIcon icon="mdi:filter-remove-outline" size={12} box={12} />
							<span class="optical-label">Reset filters</span>
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
						<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 min-[2200px]:grid-cols-5 gap-4">
							{#each [0, 1, 2] as k (k)}
								<div class="h-32 rounded-lg border border-stone-800 bg-stone-900/40 animate-pulse"></div>
							{/each}
						</div>
					</section>
				{/each}
			</div>
		{:else if error}
			<div class="rounded-lg border border-down/30 bg-down/10 px-4 py-3 flex items-start gap-2.5">
				<Icon icon="mdi:alert-circle-outline" class="w-3.5 h-3.5 text-down shrink-0 mt-0.5" />
				<p class="text-down text-sm">{error}</p>
			</div>
		{:else if filteredChallenges.length === 0}
			<div class="flex min-h-52 flex-col items-center justify-center px-4 text-center" role="status">
				<span class="mb-3 inline-flex h-8 w-8 items-center justify-center text-stone-600">
					<OpticalIcon icon={solvedOnlyEmpty ? 'mdi:check-circle-outline' : 'mdi:filter-off-outline'} size={22} box={24} />
				</span>
				<p class="text-sm font-medium text-stone-300">{solvedOnlyEmpty ? 'No solved challenges yet' : 'No matching challenges'}</p>
				<p class="mt-1 text-xs text-stone-600">
					{solvedOnlyEmpty
						? 'Completed challenges will appear here.'
						: 'Try adjusting or clearing the current filters.'}
				</p>
				{#if hasFilters}
					<button
						on:click={resetFilters}
						class="mt-4 inline-flex items-center gap-1.5 rounded-full border border-stone-800 bg-stone-900/30 px-3 py-2 text-xs leading-none text-stone-400 transition-colors hover:border-stone-700 hover:bg-stone-900/60 hover:text-stone-200"
					>
						<OpticalIcon icon="mdi:filter-remove-outline" size={13} box={14} />
						<span class="optical-label">Reset filters</span>
					</button>
				{/if}
			</div>
		{:else}
			<div class="space-y-10">
				{#each groups as group (group.category)}
					{@const color = categoryColor(group.category)}
					<section>
						<div class="flex min-h-4 items-center gap-2.5 mb-4 leading-none">
							<OpticalIcon {...catIcon(group.category)} box={16} {color} />
							<h2 class="optical-label text-[0.95rem] font-semibold text-stone-200 leading-[16px]">{group.category}</h2>
							<span class="text-[0.68rem] leading-none tabular-nums rounded-full px-2 py-0.5 border border-stone-800 bg-stone-900/40 {group.solved === group.challenges.length && $auth.isAuthenticated ? 'text-up' : 'text-stone-400'}">
								<span class="badge-label">{#if $auth.isAuthenticated}{group.solved}/{group.challenges.length}{:else}{group.challenges.length}{/if}</span>
							</span>
							<div class="flex-1 h-px bg-stone-800/60"></div>
						</div>
						<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 min-[2200px]:grid-cols-5 gap-4">
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
