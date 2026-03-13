<script lang="ts">
	import Icon from '@iconify/svelte';
	import { auth } from '$stores/auth';

	export let challenge: {
		slug: string;
		name: string;
		description?: string;
		difficulty: string;
		category?: string;
		resource_type?: string;
		base_points: number;
		total_flags: number;
		total_solves: number;
		user_solves: number;
		is_solved: boolean;
		author_name?: string;
	};

	function getDifficultyColor(difficulty: string): string {
		switch (difficulty.toLowerCase()) {
			case 'easy':
				return 'text-green-400 border-green-900 bg-green-950/30';
			case 'medium':
				return 'text-yellow-400 border-yellow-900 bg-yellow-950/30';
			case 'hard':
				return 'text-red-400 border-red-900 bg-red-950/30';
			case 'insane':
				return 'text-purple-400 border-purple-900 bg-purple-950/30';
			default:
				return 'text-stone-400 border-stone-800';
		}
	}
</script>

<a
	href="/challenges/{challenge.slug}"
	class="group bg-stone-950 border border-stone-800 rounded-lg overflow-hidden hover:border-stone-700 transition-all duration-200 {challenge.is_solved
		? 'ring-1 ring-green-900/30'
		: ''}"
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
			<span
				class="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium border {getDifficultyColor(
					challenge.difficulty
				)}"
			>
				{challenge.difficulty}
			</span>

			{#if challenge.category}
				<span
					class="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-stone-900 text-stone-400 border border-stone-800"
				>
					{challenge.category}
				</span>
			{/if}

			<!-- VM or Docker badge -->
			<span
				class="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium border {challenge.resource_type ===
				'vm'
					? 'bg-purple-950/50 text-purple-400 border-purple-800'
					: 'bg-blue-950/50 text-blue-400 border-blue-800'}"
			>
				<Icon
					icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'}
					class="w-3.5 h-3.5 mr-1"
				/>
				{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}
			</span>
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
