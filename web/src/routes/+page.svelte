<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';
	import { registerHref } from '$lib/stores/platform';

	let stats: { challenges: number | null; users: number | null; solves: number | null } = {
		challenges: null,
		users: null,
		solves: null
	};
	let statsError = '';

	onMount(async () => {
		const [challengesResult, statsResult] = await Promise.allSettled([
			api.getChallenges(),
			api.getStats()
		]);

		stats = {
			challenges: challengesResult.status === 'fulfilled'
				? challengesResult.value.challenges?.length ?? 0
				: null,
			users: statsResult.status === 'fulfilled' ? statsResult.value.total_users ?? 0 : null,
			solves: statsResult.status === 'fulfilled' ? statsResult.value.total_solves ?? 0 : null
		};
		statsError = challengesResult.status === 'rejected' || statsResult.status === 'rejected'
			? 'Some live totals are unavailable.'
			: '';
	});
</script>

<svelte:head>
	<title>Anvil</title>
</svelte:head>

<div class="fixed top-16 inset-x-0 bottom-0 overflow-hidden flex flex-col items-center justify-center text-center px-6">
	<h1 class="text-4xl sm:text-6xl md:text-7xl font-semibold tracking-tight leading-[1.05] text-stone-100">
		Forge Your<br />
		Security <span class="hero-accent">Skills</span>
	</h1>

	<p class="mt-6 text-sm md:text-base text-stone-400 max-w-xl leading-relaxed">
		Practice offensive security on realistic vulnerable machines.
		<span class="text-stone-500">Built for students and indie hackers who refuse to compromise on learning.</span>
	</p>

	<div class="mt-8 flex flex-wrap gap-3 justify-center">
		<a
			href="/challenges"
			class="inline-flex items-center gap-1.5 px-6 py-2.5 bg-stone-100 text-stone-950 text-sm leading-none font-medium hover:bg-stone-50 transition-colors rounded-full"
		>
			Get Started
			<Icon icon="mdi:arrow-right" class="w-3.5 h-3.5 shrink-0" />
		</a>
		<a
			href={$registerHref}
			class="px-6 py-2.5 border border-stone-700 text-stone-300 text-sm hover:bg-stone-800/40 hover:text-stone-100 transition-colors rounded-full"
		>
			Register
		</a>
	</div>

	<div class="mt-14 flex items-start justify-center gap-10 sm:gap-14">
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.challenges ?? '—'}</div>
			<div class="metadata-label text-stone-500 mt-1">Challenges</div>
		</div>
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.users ?? '—'}</div>
			<div class="metadata-label text-stone-500 mt-1">Users</div>
		</div>
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.solves ?? '—'}</div>
			<div class="metadata-label text-stone-500 mt-1">Solves</div>
		</div>
	</div>
	{#if statsError}
		<p class="mt-3 text-[0.7rem] text-stone-600" aria-live="polite">{statsError}</p>
	{/if}
</div>
