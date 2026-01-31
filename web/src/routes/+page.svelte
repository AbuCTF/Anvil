<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';

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
			// Fetch challenges count
			const challengesRes = await api.getChallenges();
			stats.challenges = challengesRes.challenges?.length || 0;
			
			// Fetch platform stats (public endpoint)
			try {
				const statsRes = await api.getStats();
				stats.users = statsRes.total_users || 0;
				stats.solves = statsRes.total_solves || 0;
			} catch(e) {
				// Stats not available
			}
		} catch (e) {
			// Failed to fetch data
		}
	});
</script>

<svelte:head>
	<title>{platformInfo.name}</title>
</svelte:head>

<!-- Full height landing page -->
<div class="min-h-[calc(100vh-4rem)] flex flex-col justify-center">
	<div class="max-w-4xl mx-auto px-6 sm:px-8 lg:px-12 py-8">
		<!-- Hero Content -->
		<div class="text-center space-y-6">
			<!-- Title with gradient -->
			<h1 class="text-5xl md:text-7xl font-mono font-bold tracking-tight leading-tight">
				<span class="text-white">Forge Your</span><br/>
				<span class="text-white">Security </span><span class="text-gradient-cyan font-bold">Skills</span>
			</h1>

			<!-- Subtitle -->
			<p class="text-sm md:text-base text-stone-400 font-mono max-w-2xl mx-auto leading-relaxed">
				Practice offensive security on realistic vulnerable machines.
				<span class="text-stone-500">Built for students and indie hackers who refuse to compromise on learning.</span>
			</p>

			<!-- CTA Buttons -->
			<div class="flex gap-4 justify-center pt-2">
				<a
					href="/challenges"
					class="px-8 py-3 bg-white text-black font-mono text-sm font-medium hover:bg-stone-200 transition-colors rounded-full"
				>
					Get Started →
				</a>
				<a
					href="/register"
					class="px-8 py-3 border border-stone-600 text-stone-200 font-mono text-sm hover:bg-stone-900 hover:border-stone-500 transition-colors rounded-full"
				>
					Register
				</a>
			</div>

			<!-- Stats inline -->
			<div class="flex justify-center gap-16 pt-8">
				<div class="text-center">
					<div class="text-4xl font-mono font-bold text-white">{stats.challenges}</div>
					<div class="text-stone-500 font-mono text-xs mt-1">Challenges</div>
				</div>
				<div class="text-center">
					<div class="text-4xl font-mono font-bold text-white">{stats.users}</div>
					<div class="text-stone-500 font-mono text-xs mt-1">Users</div>
				</div>
				<div class="text-center">
					<div class="text-4xl font-mono font-bold text-white">{stats.solves}</div>
					<div class="text-stone-500 font-mono text-xs mt-1">Solves</div>
				</div>
			</div>
		</div>
	</div>
</div>
