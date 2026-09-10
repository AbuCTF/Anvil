<script lang="ts">
	import Icon from '@iconify/svelte';
	import { auth } from '$stores/auth';
	import { difficultyClass, resourceClass, resourceIcon, resourceLabel } from '$lib/rank';

	export let challenge: {
		slug: string;
		name: string;
		difficulty: string;
		category?: string;
		resource_type?: string;
		base_points?: number;
		points?: number;
		total_flags: number;
		total_solves: number;
		user_solves: number;
		is_solved: boolean;
		author_name?: string;
		description?: string;
	};

	$: points = challenge.base_points ?? challenge.points ?? 0;
	$: multiFlag = challenge.total_flags > 1;
	$: progress = challenge.total_flags
		? Math.min(100, ((challenge.user_solves || 0) / challenge.total_flags) * 100)
		: 0;
</script>

<a
	href="/challenges/{challenge.slug}"
	class="tile group flex h-full flex-col rounded-lg border p-4 transition-colors duration-150 {challenge.is_solved
		? 'bg-up/[0.04] border-up/25 hover:border-up/40'
		: 'bg-stone-900/40 border-stone-800 hover:border-stone-700 hover:bg-stone-800/20'}"
>
	<div class="flex items-start justify-between gap-2">
		<h3 class="text-base font-semibold leading-snug {challenge.is_solved ? 'text-stone-200' : 'text-stone-100 group-hover:text-white'} transition-colors">
			{challenge.name}
		</h3>
		{#if challenge.is_solved}
			<Icon icon="mdi:check-circle" class="w-4 h-4 text-up shrink-0 mt-0.5" />
		{/if}
	</div>

	{#if challenge.description}
		<p class="mt-1.5 text-sm text-stone-500 leading-relaxed line-clamp-2">{challenge.description}</p>
	{/if}

	<div class="mt-3 flex flex-wrap items-center gap-2">
		<span class="inline-flex items-center rounded border px-2 py-0.5 text-[0.68rem] font-medium capitalize {difficultyClass(challenge.difficulty)}">
			{challenge.difficulty}
		</span>
		{#if challenge.resource_type}
			<span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[0.68rem] font-medium {resourceClass(challenge.resource_type)}">
				<Icon icon={resourceIcon(challenge.resource_type)} class="w-3 h-3" />
				{resourceLabel(challenge.resource_type)}
			</span>
		{/if}
	</div>

	<div class="mt-4 pt-3 border-t border-stone-800/60 flex items-center justify-between text-xs">
		<div class="flex items-center gap-3">
			<span class="inline-flex items-center gap-1 text-amber-500 font-semibold tabular-nums" title="Points">
				<Icon icon="mdi:star-outline" class="w-3.5 h-3.5" />
				{points}
			</span>
			<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Flags">
				<Icon icon="mdi:flag-outline" class="w-3.5 h-3.5" />
				{challenge.total_flags}
			</span>
		</div>
		<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Solves">
			<Icon icon="mdi:account-group" class="w-3.5 h-3.5" />
			{challenge.total_solves}
		</span>
	</div>

	{#if $auth.isAuthenticated && multiFlag}
		<div class="mt-3 pt-3 border-t border-stone-800/60">
			<div class="flex items-center justify-between text-xs mb-1.5">
				<span class="text-stone-500 uppercase tracking-wide text-[0.65rem]">Progress</span>
				<span class="text-stone-400 tabular-nums">{challenge.user_solves || 0}/{challenge.total_flags}</span>
			</div>
			<div class="w-full bg-stone-800 rounded-full h-1 overflow-hidden">
				<div class="h-full bg-up rounded-full transition-all duration-500" style="width: {progress}%"></div>
			</div>
		</div>
	{/if}
</a>

<style>
	@media (prefers-reduced-motion: no-preference) {
		.tile {
			animation: tileIn 0.2s ease both;
		}
	}
	@keyframes tileIn {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
</style>
