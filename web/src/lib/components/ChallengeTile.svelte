<script lang="ts">
	import Icon from '@iconify/svelte';
	import { auth } from '$stores/auth';

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
	};

	// Muted difficulty ramp — restrained escalation, never neon. Amber is reserved
	// for the points value, so difficulty leans on stone + muted semantic tokens.
	const diff: Record<string, string> = {
		easy: 'text-stone-400 border-stone-700 bg-stone-800/40',
		medium: 'text-info border-info/20 bg-info/10',
		hard: 'text-warn border-warn/20 bg-warn/10',
		insane: 'text-down border-down/20 bg-down/10'
	};

	$: diffClass = diff[challenge.difficulty?.toLowerCase()] ?? 'text-stone-400 border-stone-700 bg-stone-800/40';
	$: points = challenge.base_points ?? challenge.points ?? 0;
	$: multiFlag = challenge.total_flags > 1;
	$: progress = challenge.total_flags
		? Math.min(100, ((challenge.user_solves || 0) / challenge.total_flags) * 100)
		: 0;
</script>

<a
	href="/challenges/{challenge.slug}"
	class="tile group relative flex h-full flex-col rounded-lg border p-4 transition-colors duration-150 {challenge.is_solved
		? 'bg-up/[0.04] border-up/25 hover:border-up/40'
		: 'bg-stone-900/40 border-stone-800 hover:border-stone-700 hover:bg-stone-800/20'}"
>
	<div class="flex items-center justify-between gap-2 mb-3">
		<span class="inline-flex items-center rounded-full border px-2 py-0.5 text-[0.68rem] font-medium capitalize {diffClass}">
			{challenge.difficulty}
		</span>
		<div class="flex items-center gap-2 text-stone-600">
			<Icon
				icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'}
				class="w-4 h-4"
			/>
			{#if challenge.is_solved}
				<Icon icon="mdi:check-circle" class="w-4 h-4 text-up" />
			{/if}
		</div>
	</div>

	<h3 class="text-[0.95rem] font-medium leading-snug flex-1 {challenge.is_solved ? 'text-stone-200' : 'text-stone-100 group-hover:text-white'} transition-colors">
		{challenge.name}
	</h3>

	<div class="mt-4 flex items-center justify-between">
		<span class="font-semibold text-amber-500 tabular-nums text-sm">{points}<span class="text-stone-600 font-normal text-xs"> pts</span></span>
		<div class="flex items-center gap-3 text-stone-500 text-xs">
			<span class="inline-flex items-center gap-1 tabular-nums" title="Solves">
				<Icon icon="mdi:account-group" class="w-3.5 h-3.5" />
				{challenge.total_solves}
			</span>
			<span class="inline-flex items-center gap-1 tabular-nums" title="Flags">
				<Icon icon="mdi:flag-outline" class="w-3.5 h-3.5" />
				{challenge.total_flags}
			</span>
		</div>
	</div>

	{#if $auth.isAuthenticated && multiFlag}
		<div class="mt-4 pt-3 border-t border-stone-800/60">
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
