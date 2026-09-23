<script lang="ts">
	import Icon from '@iconify/svelte';
	import { auth } from '$stores/auth';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
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
		sub_description?: string;
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
		? 'challenge-tile-solved'
		: 'bg-stone-900/40 border-stone-800 hover:border-stone-700 hover:bg-stone-800/20'}"
>
	<div class="flex items-start justify-between gap-2">
		<h3 class="text-base font-semibold leading-snug {challenge.is_solved ? 'text-stone-200' : 'text-stone-100 group-hover:text-stone-50'} transition-colors">
			{challenge.name}
		</h3>
		{#if challenge.is_solved}
			<Icon icon="mdi:check-circle" class="challenge-tile-solved-mark w-4 h-4 shrink-0 mt-0.5" />
		{/if}
	</div>

	{#if challenge.sub_description}
		<p class="mt-1.5 text-sm text-stone-500 leading-relaxed line-clamp-2">{challenge.sub_description}</p>
	{/if}

	<div class="mt-3 flex flex-wrap items-center gap-2 leading-none">
		<span class="inline-flex items-center rounded border px-2 py-0.5 text-[0.68rem] leading-none font-medium capitalize {difficultyClass(challenge.difficulty)}">
			<span class="badge-label">{challenge.difficulty}</span>
		</span>
		{#if challenge.resource_type}
			<span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[0.68rem] leading-none font-medium {resourceClass(challenge.resource_type)}">
				<OpticalIcon icon={resourceIcon(challenge.resource_type)} size={12} box={12} />
				<span class="badge-label">{resourceLabel(challenge.resource_type)}</span>
			</span>
		{/if}
	</div>

	<!-- footer pinned to the card bottom so every tile in a row aligns its stats,
	     and a taller neighbour (e.g. a multi-flag progress bar) only adds breathing
	     room above rather than dead space below shorter tiles -->
	<div class="mt-auto pt-3 border-t border-stone-800/60">
		{#if $auth.isAuthenticated && multiFlag}
			<div class="mb-3">
				<div class="flex items-baseline justify-between text-xs mb-1.5">
					<span class="metadata-label text-stone-500">Progress</span>
					<span class="optical-label text-stone-400 tabular-nums">{challenge.user_solves || 0}/{challenge.total_flags}</span>
				</div>
				<div class="w-full bg-stone-800 rounded-full h-1 overflow-hidden">
					<div class="h-full bg-up rounded-full transition-all duration-500" style="width: {progress}%"></div>
				</div>
			</div>
		{/if}
		<div class="flex items-center justify-between text-xs leading-none">
			<div class="flex items-center gap-3">
				<span class="inline-flex items-center gap-1 text-amber-500 font-semibold tabular-nums" title="Points">
					<OpticalIcon icon="mdi:star-outline" size={12} box={12} />
					<span class="optical-label">{points}</span>
				</span>
				<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Flags">
					<OpticalIcon icon="mdi:flag-outline" size={12} box={12} />
					<span class="optical-label">{challenge.total_flags}</span>
				</span>
			</div>
			<span class="inline-flex items-center gap-1 text-stone-500 tabular-nums" title="Solves">
				<OpticalIcon icon="mdi:account-group" size={12} box={12} />
				<span class="optical-label">{challenge.total_solves}</span>
			</span>
		</div>
	</div>
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
