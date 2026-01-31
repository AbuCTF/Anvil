<script lang="ts">
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
			const response = await api.getScoreboard();
			entries = response.entries || [];
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
</script>

<svelte:head>
	<title>Scoreboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8 font-mono">
	<div class="flex items-center justify-between mb-8">
		<div>
			<h1 class="text-2xl font-bold text-stone-100">SCOREBOARD</h1>
			<p class="mt-1 text-stone-500">
				{totalUsers} participants
			</p>
		</div>
	</div>

	{#if loading}
		<div class="flex items-center justify-center py-12">
			<p class="text-stone-500">LOADING...</p>
		</div>
	{:else if error}
		<div class="bg-stone-900 border border-stone-800 p-6 text-center">
			<p class="text-stone-300">{error}</p>
		</div>
	{:else if entries.length === 0}
		<div class="bg-stone-900 border border-stone-800 p-12 text-center">
			<h3 class="text-xl font-medium text-stone-100 mb-2">NO SCORES YET</h3>
			<p class="text-stone-500">Be the first to solve a challenge!</p>
		</div>
	{:else}
		{#if entries.length >= 3}
			<div class="grid grid-cols-3 gap-4 mb-8">
				<div class="order-1 col-span-1">
					<div class="bg-stone-900 border border-stone-800 p-6 text-center mt-8">
						<div class="text-2xl font-bold text-stone-100">2</div>
						<div class="text-lg font-medium text-stone-100 mt-2">
							{entries[1].display_name || entries[1].team_name || entries[1].username}
						</div>
						<div class="text-stone-500 font-semibold">{entries[1].total_score} pts</div>
					</div>
				</div>

				<div class="order-2 col-span-1">
					<div class="bg-stone-900 border border-stone-700 p-6 text-center">
						<div class="text-3xl font-bold text-stone-100">1</div>
						<div class="text-xl font-medium text-stone-100 mt-2">
							{entries[0].display_name || entries[0].team_name || entries[0].username}
						</div>
						<div class="text-stone-300 font-bold text-lg">{entries[0].total_score} pts</div>
					</div>
				</div>

				<div class="order-3 col-span-1">
					<div class="bg-stone-900 border border-stone-800 p-6 text-center mt-12">
						<div class="text-2xl font-bold text-stone-100">3</div>
						<div class="text-lg font-medium text-stone-100 mt-2">
							{entries[2].display_name || entries[2].team_name || entries[2].username}
						</div>
						<div class="text-stone-500 font-semibold">{entries[2].total_score} pts</div>
					</div>
				</div>
			</div>
		{/if}

		<div class="bg-stone-900 border border-stone-800 overflow-hidden">
			<table class="w-full">
				<thead>
					<tr class="border-b border-stone-800">
						<th class="px-6 py-3 text-left text-xs font-medium text-stone-500 uppercase">Rank</th>
						<th class="px-6 py-3 text-left text-xs font-medium text-stone-500 uppercase">User</th>
						<th class="px-6 py-3 text-right text-xs font-medium text-stone-500 uppercase">Score</th>
						<th class="px-6 py-3 text-right text-xs font-medium text-stone-500 uppercase hidden md:table-cell">
							Challenges
						</th>
						<th class="px-6 py-3 text-right text-xs font-medium text-stone-500 uppercase hidden lg:table-cell">
							Flags
						</th>
						<th class="px-6 py-3 text-right text-xs font-medium text-stone-500 uppercase hidden lg:table-cell">
							Last Solve
						</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-stone-800">
					{#each entries as entry}
						<tr class="hover:bg-stone-800/50">
							<td class="px-6 py-4 whitespace-nowrap">
								<span class="text-stone-300 font-medium">#{entry.rank}</span>
							</td>
							<td class="px-6 py-4 whitespace-nowrap">
								<div class="flex items-center">
									<div class="w-8 h-8 bg-stone-800 flex items-center justify-center mr-3">
										<span class="text-stone-500 text-sm">{(entry.display_name || entry.team_name || entry.username).charAt(0).toUpperCase()}</span>
									</div>
									<div>
										<div class="text-stone-100 font-medium">
											{entry.display_name || entry.team_name || entry.username}
										</div>
										{#if entry.team_name && entry.username}
											<div class="text-stone-600 text-sm">@{entry.username}</div>
										{/if}
									</div>
								</div>
							</td>
							<td class="px-6 py-4 whitespace-nowrap text-right">
								<span class="text-stone-300 font-semibold">{entry.total_score}</span>
							</td>
							<td class="px-6 py-4 whitespace-nowrap text-right text-stone-400 hidden md:table-cell">
								{entry.challenges_solved}
							</td>
							<td class="px-6 py-4 whitespace-nowrap text-right text-stone-400 hidden lg:table-cell">
								{entry.flags_solved}
							</td>
							<td class="px-6 py-4 whitespace-nowrap text-right text-stone-500 text-sm hidden lg:table-cell">
								{formatDate(entry.last_solve_at)}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
