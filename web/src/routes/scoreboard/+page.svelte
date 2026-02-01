<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';

	interface ScoreboardEntry {
		rank: number;
		username: string;
		display_name?: string;
		team_name?: string;
		total_score: number;
		challenges_solved: number;
		flags_solved: number;
		last_solve_at?: string;
	}

	let entries: ScoreboardEntry[] = [];
	let totalUsers = 0;
	let loading = true;
	let error = '';

	onMount(async () => {
		try {
			const response = await api.getScoreboard() as { leaderboard: ScoreboardEntry[]; total_users: number };
			entries = response.leaderboard || [];
			totalUsers = response.total_users || 0;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load scoreboard';
		} finally {
			loading = false;
		}
	});

	function formatDate(dateString?: string) {
		if (!dateString) return 'N/A';
		return new Date(dateString).toLocaleString();
	}

	function getRankIcon(rank: number) {
		switch (rank) {
			case 1:
				return { icon: 'mdi:trophy', color: 'text-yellow-500' };
			case 2:
				return { icon: 'mdi:medal', color: 'text-gray-400' };
			case 3:
				return { icon: 'mdi:medal', color: 'text-amber-600' };
			default:
				return null;
		}
	}
</script>

<svelte:head>
	<title>Scoreboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<!-- Header -->
	<div class="flex items-center justify-between mb-8">
		<div>
			<h1 class="text-2xl sm:text-3xl font-bold text-white">Scoreboard</h1>
			<p class="mt-1 text-stone-400">
				{totalUsers} participants
			</p>
		</div>
	</div>

	{#if loading}
		<div class="flex items-center justify-center py-12">
			<Icon icon="mdi:loading" class="w-8 h-8 text-amber-500 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-red-500/10 border border-red-500/20 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle" class="w-12 h-12 text-red-500 mx-auto mb-4" />
			<p class="text-red-400">{error}</p>
		</div>
	{:else if entries.length === 0}
		<div class="bg-stone-900/50 rounded-lg p-12 text-center">
			<Icon icon="mdi:trophy-outline" class="w-16 h-16 text-stone-600 mx-auto mb-4" />
			<h3 class="text-xl font-medium text-white mb-2">No scores yet</h3>
			<p class="text-stone-400">Be the first to solve a challenge!</p>
		</div>
	{:else}
		<!-- Top 3 Podium -->
		{#if entries.length >= 3}
			<!-- Mobile: Stacked view -->
			<div class="md:hidden space-y-3 mb-8">
				{#each entries.slice(0, 3) as entry, i}
					{@const medals = ['🥇', '🥈', '🥉']}
					<div class="bg-stone-900/50 rounded-lg p-4 border border-stone-800 flex items-center gap-4">
						<span class="text-2xl">{medals[i]}</span>
						<div class="flex-1">
							<p class="text-white font-medium">{entry.display_name || entry.team_name || entry.username}</p>
							<p class="text-xs text-stone-500">{entry.challenges_solved} challenges</p>
						</div>
						<span class="text-amber-500 font-bold">{entry.total_score} pts</span>
					</div>
				{/each}
			</div>

			<!-- Desktop: Podium view -->
			<div class="hidden md:grid grid-cols-3 gap-4 mb-8">
				<!-- 2nd Place -->
				<div class="order-1 col-span-1">
					<div class="bg-stone-900/50 rounded-xl p-6 border border-gray-400/30 text-center mt-8">
						<Icon icon="mdi:medal" class="w-10 h-10 text-gray-400 mx-auto mb-2" />
						<div class="text-2xl font-bold text-white">2nd</div>
						<div class="text-lg font-medium text-white mt-2">
							{entries[1].display_name || entries[1].team_name || entries[1].username}
						</div>
						<div class="text-amber-500 font-semibold">{entries[1].total_score} pts</div>
					</div>
				</div>

				<!-- 1st Place -->
				<div class="order-2 col-span-1">
					<div class="bg-gradient-to-b from-yellow-500/20 to-stone-900/50 rounded-xl p-6 border border-yellow-500/30 text-center">
						<Icon icon="mdi:trophy" class="w-12 h-12 text-yellow-500 mx-auto mb-2" />
						<div class="text-3xl font-bold text-white">1st</div>
						<div class="text-xl font-medium text-white mt-2">
							{entries[0].display_name || entries[0].team_name || entries[0].username}
						</div>
						<div class="text-amber-500 font-bold text-lg">{entries[0].total_score} pts</div>
					</div>
				</div>

				<!-- 3rd Place -->
				<div class="order-3 col-span-1">
					<div class="bg-stone-900/50 rounded-xl p-6 border border-amber-600/30 text-center mt-12">
						<Icon icon="mdi:medal" class="w-10 h-10 text-amber-600 mx-auto mb-2" />
						<div class="text-2xl font-bold text-white">3rd</div>
						<div class="text-lg font-medium text-white mt-2">
							{entries[2].display_name || entries[2].team_name || entries[2].username}
						</div>
						<div class="text-amber-500 font-semibold">{entries[2].total_score} pts</div>
					</div>
				</div>
			</div>
		{/if}

		<!-- Full Scoreboard Table -->
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
			<table class="w-full">
				<thead>
					<tr class="border-b border-stone-800">
						<th class="px-4 sm:px-6 py-4 text-left text-sm font-medium text-stone-400">Rank</th>
						<th class="px-4 sm:px-6 py-4 text-left text-sm font-medium text-stone-400">User</th>
						<th class="px-4 sm:px-6 py-4 text-right text-sm font-medium text-stone-400">Score</th>
						<th class="px-4 sm:px-6 py-4 text-right text-sm font-medium text-stone-400 hidden md:table-cell">
							Challenges
						</th>
						<th class="px-4 sm:px-6 py-4 text-right text-sm font-medium text-stone-400 hidden lg:table-cell">
							Flags
						</th>
						<th class="px-4 sm:px-6 py-4 text-right text-sm font-medium text-stone-400 hidden lg:table-cell">
							Last Solve
						</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-stone-800">
					{#each entries as entry, index}
						{@const rankInfo = getRankIcon(entry.rank)}
						<tr class="hover:bg-stone-800/50 transition-colors">
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap">
								<div class="flex items-center">
									{#if rankInfo}
										<Icon icon={rankInfo.icon} class="w-5 h-5 {rankInfo.color} mr-2" />
									{/if}
									<span class="text-white font-medium">#{entry.rank}</span>
								</div>
							</td>
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap">
								<div class="flex items-center">
									<div class="w-8 h-8 bg-stone-800 rounded-full flex items-center justify-center mr-3 hidden sm:flex">
										<Icon icon="mdi:account" class="w-5 h-5 text-stone-400" />
									</div>
									<div>
										<div class="text-white font-medium text-sm sm:text-base truncate max-w-[120px] sm:max-w-none">
											{entry.display_name || entry.team_name || entry.username}
										</div>
										{#if entry.team_name && entry.username}
											<div class="text-stone-500 text-sm hidden sm:block">@{entry.username}</div>
										{/if}
									</div>
								</div>
							</td>
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap text-right">
								<span class="text-amber-500 font-semibold">{entry.total_score}</span>
							</td>
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap text-right text-stone-300 hidden md:table-cell">
								{entry.challenges_solved}
							</td>
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap text-right text-stone-300 hidden lg:table-cell">
								{entry.flags_solved}
							</td>
							<td class="px-4 sm:px-6 py-4 whitespace-nowrap text-right text-stone-400 text-sm hidden lg:table-cell">
								{formatDate(entry.last_solve_at)}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
