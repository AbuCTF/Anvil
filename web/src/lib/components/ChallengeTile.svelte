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

	const diff: Record<string, string> = {
		easy: 'text-green-400 bg-green-950/30 border-green-900',
		medium: 'text-yellow-400 bg-yellow-950/30 border-yellow-900',
		hard: 'text-orange-400 bg-orange-950/30 border-orange-900',
		insane: 'text-purple-400 bg-purple-950/30 border-purple-900'
	};

	$: diffClass = diff[challenge.difficulty?.toLowerCase()] ?? 'text-stone-400 bg-stone-900/40 border-stone-800';
	$: points = challenge.base_points ?? challenge.points ?? 0;
	$: multiFlag = challenge.total_flags > 1;
	$: progress = challenge.total_flags
		? Math.min(100, ((challenge.user_solves || 0) / challenge.total_flags) * 100)
		: 0;
</script>

<a
	href="/challenges/{challenge.slug}"
	class="tile group relative flex h-full flex-col rounded-lg border p-5 transition-all duration-200 hover:-translate-y-0.5 {challenge.is_solved
		? 'bg-green-950/10 border-green-900/50 ring-1 ring-green-900/40'
		: 'bg-stone-950 border-stone-800 hover:border-stone-700'}"
>
	<div class="flex items-center justify-between gap-2 mb-3">
		<span class="inline-flex items-center px-2 py-0.5 rounded text-[0.7rem] font-medium border capitalize {diffClass}">
			{challenge.difficulty}
		</span>
		<div class="flex items-center gap-2 text-stone-600">
			<Icon
				icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'}
				class="w-4 h-4"
			/>
			{#if challenge.is_solved}
				<Icon icon="mdi:check-circle" class="w-4 h-4 text-green-500" />
			{/if}
		</div>
	</div>

	<h3 class="text-base font-semibold leading-snug flex-1 {challenge.is_solved ? 'text-green-300' : 'text-white group-hover:text-amber-100'} transition">
		{challenge.name}
	</h3>

	<div class="mt-4 flex items-center justify-between text-sm">
		<span class="font-bold text-amber-500 tabular-nums">{points}<span class="text-stone-600 font-normal text-xs"> pts</span></span>
		<div class="flex items-center gap-3 text-stone-500 text-xs">
			<span class="inline-flex items-center gap-1" title="Solves">
				<Icon icon="mdi:account-group" class="w-3.5 h-3.5" />
				{challenge.total_solves}
			</span>
			<span class="inline-flex items-center gap-1" title="Flags">
				<Icon icon="mdi:flag-outline" class="w-3.5 h-3.5" />
				{challenge.total_flags}
			</span>
		</div>
	</div>

	{#if $auth.isAuthenticated && multiFlag}
		<div class="mt-4 pt-4 border-t border-stone-800/70">
			<div class="flex items-center justify-between text-xs mb-1.5">
				<span class="text-stone-500">Progress</span>
				<span class="text-stone-400 tabular-nums">{challenge.user_solves || 0}/{challenge.total_flags}</span>
			</div>
			<div class="w-full bg-stone-900 rounded-full h-1.5 overflow-hidden">
				<div class="h-full bg-green-500 rounded-full transition-all duration-500" style="width: {progress}%"></div>
			</div>
		</div>
	{/if}
</a>

<style>
	@media (prefers-reduced-motion: no-preference) {
		.tile {
			animation: tileIn 0.25s ease both;
		}
	}
	@keyframes tileIn {
		from {
			opacity: 0;
			transform: translateY(6px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
</style>
