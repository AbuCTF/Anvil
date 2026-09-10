<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';
	import Card from '$lib/components/Card.svelte';
	import StatTile from '$lib/components/StatTile.svelte';

	let platformInfo = {
		name: 'Anvil',
		description: 'Forge your skills'
	};

	let stats = {
		challenges: 0,
		users: 0,
		solves: 0
	};

	onMount(async () => {
		try {
			// Fetch data in parallel for faster loading
			const [challengesRes, statsRes] = await Promise.all([
				api.getChallenges().catch(() => ({ challenges: [] })),
				api.getStats().catch(() => ({ total_users: 0, total_solves: 0 }))
			]);

			stats.challenges = challengesRes.challenges?.length || 0;
			stats.users = statsRes.total_users || 0;
			stats.solves = statsRes.total_solves || 0;
		} catch (e) {
			// Failed to fetch data
		}
	});

	const features = [
		{
			icon: 'mdi:server-network',
			title: 'On-demand Instances',
			body: 'Spin up isolated, disposable machines on demand. Attack a fresh target and tear it down when you are done.'
		},
		{
			icon: 'mdi:flag-variant-outline',
			title: 'Realistic Challenges',
			body: 'Vulnerable-by-design boxes drawn from real-world misconfigurations and exploits, not toy puzzles.'
		},
		{
			icon: 'mdi:chart-timeline-variant',
			title: 'Live Scoreboard',
			body: 'Track solves, ranks, and first bloods as they land. Compete solo or measure yourself against the field.'
		}
	];
</script>

<svelte:head>
	<title>{platformInfo.name}</title>
</svelte:head>

<div class="max-w-5xl mx-auto px-6 sm:px-8 lg:px-12 py-20 sm:py-28">
	<!-- Hero -->
	<section class="text-center space-y-6 max-w-3xl mx-auto">
		<div class="text-xs font-mono uppercase tracking-widest text-stone-500">
			Offensive security training
		</div>

		<h1 class="text-5xl md:text-7xl font-mono font-semibold tracking-tight leading-[1.05] text-stone-100">
			Forge Your<br />
			Security <span class="text-amber-500">Skills</span>
		</h1>

		<p class="text-sm md:text-base text-stone-400 font-mono max-w-2xl mx-auto leading-relaxed">
			Practice offensive security on realistic vulnerable machines.
			<span class="text-stone-500">Built for students and indie hackers who refuse to compromise on learning.</span>
		</p>

		<div class="flex flex-wrap gap-3 justify-center pt-2">
			<a
				href="/challenges"
				class="px-6 py-2.5 bg-stone-100 text-stone-950 font-mono text-sm font-medium hover:bg-white transition-colors rounded-md"
			>
				Get Started →
			</a>
			<a
				href="/register"
				class="px-6 py-2.5 border border-stone-800 text-stone-300 font-mono text-sm hover:bg-stone-800/40 hover:text-stone-100 transition-colors rounded-md"
			>
				Register
			</a>
		</div>
	</section>

	<!-- Stats -->
	<section class="mt-16 grid grid-cols-3 gap-3 max-w-2xl mx-auto">
		<StatTile label="Challenges" value={stats.challenges} />
		<StatTile label="Users" value={stats.users} />
		<StatTile label="Solves" value={stats.solves} />
	</section>

	<!-- Features -->
	<section class="mt-16">
		<div class="text-xs font-mono uppercase tracking-widest text-stone-500 text-center mb-5">
			Built for practice
		</div>
		<div class="grid grid-cols-1 md:grid-cols-3 gap-4">
			{#each features as feature}
				<Card>
					<div slot="header" class="flex items-center gap-2">
						<Icon icon={feature.icon} class="w-4 h-4 text-stone-500" />
						<h2 class="text-sm font-semibold text-stone-200 uppercase tracking-wide">{feature.title}</h2>
					</div>
					<p class="text-sm text-stone-400 leading-relaxed">{feature.body}</p>
				</Card>
			{/each}
		</div>
	</section>
</div>
