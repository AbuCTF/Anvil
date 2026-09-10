<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';

	let stats = { challenges: 0, users: 0, solves: 0 };

	onMount(async () => {
		try {
			const [challengesRes, statsRes] = await Promise.all([
				api.getChallenges().catch(() => ({ challenges: [] })),
				api.getStats().catch(() => ({ total_users: 0, total_solves: 0 }))
			]);
			stats.challenges = challengesRes.challenges?.length || 0;
			stats.users = statsRes.total_users || 0;
			stats.solves = statsRes.total_solves || 0;
		} catch (e) {
			/* ignore */
		}
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
			class="inline-flex items-center gap-1.5 px-6 py-2.5 bg-stone-100 text-stone-950 text-sm font-medium hover:bg-stone-50 transition-colors rounded-full"
		>
			Get Started
			<Icon icon="mdi:arrow-right" class="w-4 h-4 -translate-y-[2px]" />
		</a>
		<a
			href="/register"
			class="px-6 py-2.5 border border-stone-700 text-stone-300 text-sm hover:bg-stone-800/40 hover:text-stone-100 transition-colors rounded-full"
		>
			Register
		</a>
	</div>

	<div class="mt-14 flex items-start justify-center gap-10 sm:gap-14">
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.challenges}</div>
			<div class="text-xs text-stone-500 mt-1">Challenges</div>
		</div>
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.users}</div>
			<div class="text-xs text-stone-500 mt-1">Users</div>
		</div>
		<div>
			<div class="text-3xl font-semibold text-stone-100 tabular-nums">{stats.solves}</div>
			<div class="text-xs text-stone-500 mt-1">Solves</div>
		</div>
	</div>
</div>
