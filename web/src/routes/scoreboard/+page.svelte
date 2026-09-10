<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { api } from '$api';
	import { API_BASE } from '$lib/config';
	import LineChart from '$lib/components/LineChart.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import { teamColor, rankAccent } from '$lib/rank';
	import type { Series } from '$lib/chart/path';

	interface ScoreboardEntry {
		rank: number;
		user_id: string;
		username: string;
		display_name?: string;
		team_name?: string;
		total_score: number;
		challenges_solved: number;
		flags_solved: number;
		last_solve_at?: string;
	}

	interface HistorySeries {
		id: string;
		label: string;
		points: { x: number; y: number }[];
	}

	const POLL_MS = 5000;

	let entries: ScoreboardEntry[] = [];
	let totalUsers = 0;
	let raceSeries: Series[] = [];
	let sparks: Record<string, number[]> = {};

	let loading = true;
	let error = '';
	let inFlight = false;
	let timer: ReturnType<typeof setInterval>;

	async function load() {
		if (inFlight) return;
		inFlight = true;
		try {
			const [sbRaw, histRaw] = await Promise.all([
				api.getScoreboard(),
				fetch(`${API_BASE}/api/v1/scoreboard/history`)
					.then((r) => (r.ok ? r.json() : { series: [] }))
					.catch(() => ({ series: [] }))
			]);

			const sb = sbRaw as { leaderboard: ScoreboardEntry[]; total_users: number };
			const hist = histRaw as { series: HistorySeries[] };

			entries = sb.leaderboard ?? [];
			totalUsers = sb.total_users ?? 0;

			const series = hist.series ?? [];
			raceSeries = series.map((s) => ({
				label: s.label,
				color: teamColor(s.id),
				points: s.points
			}));
			sparks = Object.fromEntries(series.map((s) => [s.id, s.points.map((p) => p.y)]));
			error = '';
		} catch (e) {
			// Keep the last good board on a transient error (rate-limit / blip); retry next poll.
			if (entries.length === 0) error = e instanceof Error ? e.message : 'Failed to load scoreboard';
		} finally {
			loading = false;
			inFlight = false;
		}
	}

	onMount(() => {
		load();
		timer = setInterval(load, POLL_MS);
		return () => clearInterval(timer);
	});

	$: podium =
		entries.length >= 3
			? [
					{ e: entries[1], place: '2nd', cls: 'border-stone-600/40 bg-stone-900/50 mt-8' },
					{
						e: entries[0],
						place: '1st',
						cls: 'border-yellow-500/40 bg-gradient-to-b from-yellow-500/10 to-stone-900/50'
					},
					{ e: entries[2], place: '3rd', cls: 'border-amber-600/40 bg-stone-900/50 mt-12' }
				]
			: [];

	function displayName(e: ScoreboardEntry) {
		return e.display_name || e.team_name || e.username;
	}

	function hasAlias(e: ScoreboardEntry) {
		return !!(e.display_name || e.team_name);
	}

	function formatDate(dateString?: string) {
		if (!dateString) return '—';
		return new Date(dateString).toLocaleString();
	}

	function tierIcon(rank: number): string | null {
		if (rank === 1) return 'mdi:trophy';
		if (rank <= 3) return 'mdi:medal';
		return null;
	}
</script>

<svelte:head>
	<title>Scoreboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<div class="flex flex-wrap items-end justify-between gap-4 mb-8">
		<div>
			<h1 class="text-2xl sm:text-3xl font-bold text-white">Scoreboard</h1>
			<p class="mt-1 text-stone-400 tabular-nums">{totalUsers} participants</p>
		</div>
		{#if !loading && !error}
			<div class="flex items-center gap-2 text-stone-600 text-xs">
				<span class="w-2 h-2 rounded-full bg-green-500 animate-pulse"></span>
				live · {POLL_MS / 1000}s
			</div>
		{/if}
	</div>

	{#if loading}
		<div class="flex items-center justify-center py-12">
			<Icon icon="mdi:loading" class="w-8 h-8 text-amber-500 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-red-500/10 border border-red-500/20 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle" class="w-12 h-12 text-red-500 mx-auto mb-4" />
			<p class="text-red-400">{error}</p>
			<p class="text-stone-500 text-sm mt-2">Retrying every {POLL_MS / 1000}s…</p>
		</div>
	{:else if entries.length === 0}
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-12 text-center">
			<Icon icon="mdi:trophy-outline" class="w-16 h-16 text-stone-600 mx-auto mb-4" />
			<h3 class="text-xl font-medium text-white mb-2">No scores yet</h3>
			<p class="text-stone-400">Be the first to solve a challenge!</p>
		</div>
	{:else}
		{#if raceSeries.length}
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden mb-8">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:chart-line" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Score over time</h2>
					<span class="text-stone-500 text-sm ml-auto">top {raceSeries.length}</span>
				</div>
				<div class="p-4">
					<LineChart series={raceSeries} height={300} />
					<div class="flex flex-wrap gap-x-4 gap-y-1 mt-3">
						{#each raceSeries as s}
							<span class="inline-flex items-center gap-1.5 text-xs text-stone-400">
								<span class="w-2.5 h-2.5 rounded-full" style="background: {s.color};"></span>{s.label}
							</span>
						{/each}
					</div>
				</div>
			</div>
		{/if}

		{#if entries.length >= 3}
			<div class="md:hidden space-y-3 mb-8">
				{#each entries.slice(0, 3) as entry}
					{@const c = teamColor(entry.user_id)}
					<div class="bg-stone-900/50 rounded-xl p-4 border border-stone-800 flex items-center gap-4">
						<Icon icon={tierIcon(entry.rank) ?? 'mdi:medal'} class="w-7 h-7 {rankAccent(entry.rank)}" />
						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2">
								<span class="w-2.5 h-2.5 rounded-full shrink-0" style="background: {c};"></span>
								<a href="/profile/{entry.username}" class="block text-white font-medium truncate hover:text-amber-400 transition">{displayName(entry)}</a>
							</div>
							<p class="text-xs text-stone-500 tabular-nums">{entry.challenges_solved} challenges</p>
						</div>
						<span class="text-amber-500 font-bold tabular-nums">{entry.total_score}</span>
					</div>
				{/each}
			</div>

			<div class="hidden md:grid grid-cols-3 gap-4 mb-8 items-end">
				{#each podium as p}
					{@const c = teamColor(p.e.user_id)}
					<div class="rounded-xl border p-6 text-center {p.cls}">
						<Icon icon={tierIcon(p.e.rank) ?? 'mdi:medal'} class="w-10 h-10 mx-auto mb-2 {rankAccent(p.e.rank)}" />
						<div class="text-2xl font-bold text-white">{p.place}</div>
						<div class="flex items-center justify-center gap-2 mt-2">
							<span class="w-2.5 h-2.5 rounded-full shrink-0" style="background: {c};"></span>
							<a href="/profile/{p.e.username}" class="text-lg font-medium text-white truncate hover:text-amber-400 transition">{displayName(p.e)}</a>
						</div>
						<div class="text-amber-500 font-bold tabular-nums mt-1">{p.e.total_score} pts</div>
						<div class="text-xs text-stone-500 tabular-nums mt-0.5">
							{p.e.challenges_solved} challenges · {p.e.flags_solved} flags
						</div>
					</div>
				{/each}
			</div>
		{/if}

		<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
			<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
				<Icon icon="mdi:trophy" class="w-5 h-5 text-amber-500" />
				<h2 class="text-lg font-semibold text-white">Standings</h2>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full min-w-[640px]">
					<thead>
						<tr class="border-b border-stone-800 text-stone-400 text-sm">
							<th class="px-4 sm:px-6 py-3 text-left font-medium w-16">Rank</th>
							<th class="px-4 sm:px-6 py-3 text-left font-medium">Player</th>
							<th class="px-4 sm:px-6 py-3 text-left font-medium hidden sm:table-cell">Trend</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Score</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium hidden md:table-cell">Challenges</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium hidden lg:table-cell">Flags</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium hidden xl:table-cell">Last Solve</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-800">
						{#each entries as entry (entry.user_id)}
							{@const c = teamColor(entry.user_id)}
							{@const ti = tierIcon(entry.rank)}
							<tr class="hover:bg-stone-800/40 transition-colors {entry.rank === 1 ? 'bg-amber-500/5' : ''}">
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap">
									<div class="flex items-center gap-1.5">
										{#if ti}
											<Icon icon={ti} class="w-5 h-5 {rankAccent(entry.rank)}" />
										{/if}
										<span class="text-white font-bold tabular-nums">{entry.rank}</span>
									</div>
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap">
									<div class="flex items-center gap-2.5">
										<span class="w-3 h-3 rounded-full shrink-0" style="background: {c};"></span>
										<div class="min-w-0">
											<a
												href="/profile/{entry.username}"
												class="block text-white font-medium truncate max-w-[160px] sm:max-w-[240px] hover:text-amber-400 transition"
											>
												{displayName(entry)}
											</a>
											{#if hasAlias(entry)}
												<div class="text-stone-500 text-xs hidden sm:block">@{entry.username}</div>
											{/if}
										</div>
									</div>
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap hidden sm:table-cell">
									<Sparkline data={sparks[entry.user_id] ?? []} color={c} />
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-amber-500 font-bold tabular-nums">
									{entry.total_score}
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums hidden md:table-cell">
									{entry.challenges_solved}
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums hidden lg:table-cell">
									{entry.flags_solved}
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-400 text-sm hidden xl:table-cell">
									{formatDate(entry.last_solve_at)}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>
