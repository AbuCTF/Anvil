<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { API_BASE } from '$lib/config';
	import LineChart from '$lib/components/LineChart.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { teamColor, rankAccent } from '$lib/rank';
	import { downloadRankCard } from '$lib/share';
	import type { Series } from '$lib/chart/path';

	interface Entry {
		rank: number;
		user_id: string;
		username: string;
		display_name?: string;
		total_score: number;
		challenges_solved: number;
		flags_solved: number;
		last_solve_at?: string;
		delta: number;
		spark?: number[];
	}
	interface HistorySeries {
		id: string;
		label: string;
		points: { x: number; y: number }[];
	}

	const POLL_MS = 5000;

	let entries: Entry[] = [];
	let totalUsers = 0;
	let raceSeries: Series[] = [];
	let loading = true;
	let error = '';
	let inFlight = false;
	let timer: ReturnType<typeof setInterval>;

	let search = '';
	let sortKey: 'rank' | 'name' = 'rank';

	async function load() {
		if (inFlight) return;
		inFlight = true;
		try {
			const [sbRes, histRes] = await Promise.all([
				fetch(`${API_BASE}/api/v1/scoreboard?limit=500`).then((r) => (r.ok ? r.json() : Promise.reject(r.status))),
				fetch(`${API_BASE}/api/v1/scoreboard/history`)
					.then((r) => (r.ok ? r.json() : { series: [] }))
					.catch(() => ({ series: [] }))
			]);

			entries = sbRes.leaderboard ?? [];
			totalUsers = sbRes.total_users ?? entries.length;

			const series: HistorySeries[] = histRes.series ?? [];
			raceSeries = series.map((s) => ({ label: s.label, color: teamColor(s.id), points: s.points }));
			error = '';
		} catch (e) {
			if (entries.length === 0) error = typeof e === 'number' ? `HTTP ${e}` : 'Failed to load scoreboard';
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

	const displayName = (e: Entry) => e.display_name || e.username;

	$: leaderIdx = entries.length ? raceSeries.findIndex((s) => s.label === entries[0].username) : -1;

	$: filtered = (() => {
		const q = search.trim().toLowerCase();
		let rows = q ? entries.filter((e) => displayName(e).toLowerCase().includes(q) || e.username.toLowerCase().includes(q)) : entries;
		if (sortKey === 'name') rows = [...rows].sort((a, b) => displayName(a).localeCompare(displayName(b)));
		return rows;
	})();

	function formatDate(s?: string) {
		if (!s) return '—';
		return new Date(s).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}
	function tierIcon(rank: number): string | null {
		if (rank === 1) return 'mdi:trophy';
		if (rank <= 3) return 'mdi:medal';
		return null;
	}
	const clock = (x: number) => new Date(x * 1000).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });

	function share(e: Entry) {
		downloadRankCard({
			rank: e.rank,
			username: displayName(e),
			score: e.total_score,
			solves: e.challenges_solved,
			delta: e.delta,
			spark: e.spark ?? [],
			color: teamColor(e.user_id)
		});
	}
</script>

<svelte:head>
	<title>Scoreboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<PageHeader title="Scoreboard" subtitle="{totalUsers} participants">
		<div slot="actions" class="flex items-center gap-2">
			<div class="relative">
				<Icon icon="mdi:magnify" class="w-4 h-4 text-stone-600 absolute left-2.5 top-1/2 -translate-y-1/2" />
				<input
					bind:value={search}
					placeholder="Search"
					class="w-40 sm:w-52 bg-stone-900/60 border border-stone-800 rounded-md pl-8 pr-3 py-1.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-700"
				/>
			</div>
			<div class="flex rounded-md border border-stone-800 overflow-hidden text-xs">
				<button
					class="px-2.5 py-1.5 transition-colors {sortKey === 'rank' ? 'bg-stone-800 text-stone-200' : 'text-stone-500 hover:text-stone-300'}"
					on:click={() => (sortKey = 'rank')}>Rank</button
				>
				<button
					class="px-2.5 py-1.5 transition-colors border-l border-stone-800 {sortKey === 'name' ? 'bg-stone-800 text-stone-200' : 'text-stone-500 hover:text-stone-300'}"
					on:click={() => (sortKey = 'name')}>Name</button
				>
			</div>
		</div>
	</PageHeader>

	{#if loading}
		<div class="flex items-center justify-center py-16">
			<Icon icon="mdi:loading" class="w-6 h-6 text-stone-500 animate-spin" />
		</div>
	{:else if error}
		<EmptyState icon="mdi:alert-circle-outline" text={error} />
	{:else if entries.length === 0}
		<EmptyState icon="mdi:trophy-outline" text="No scores yet." />
	{:else}
		{#if raceSeries.length}
			<div class="mb-6">
				<Card title="Score over time">
					<span slot="meta" class="text-stone-500 text-xs">top {raceSeries.length}</span>
					<LineChart series={raceSeries} height={280} curve="step" emphasize={leaderIdx} xFormat={clock} />
				</Card>
			</div>
		{/if}

		<Card title="Standings" bodyClass="">
			<span slot="meta" class="text-stone-500 text-xs tabular-nums">{filtered.length}</span>
			<div class="overflow-x-auto">
				<table class="w-full min-w-[680px] text-sm">
					<thead>
						<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
							<th class="px-4 py-2.5 text-left font-medium w-16">Rank</th>
							<th class="px-4 py-2.5 text-left font-medium">Player</th>
							<th class="px-4 py-2.5 text-left font-medium hidden sm:table-cell w-28">Trend</th>
							<th class="px-4 py-2.5 text-right font-medium hidden md:table-cell">Solves</th>
							<th class="px-4 py-2.5 text-right font-medium">Score</th>
							<th class="px-4 py-2.5 text-right font-medium hidden xl:table-cell">Last solve</th>
							<th class="px-3 py-2.5 w-10"></th>
						</tr>
					</thead>
					<tbody>
						{#each filtered as e (e.user_id)}
							{@const c = teamColor(e.user_id)}
							{@const ti = tierIcon(e.rank)}
							<tr class="group border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors {e.rank === 1 ? 'bg-amber-500/[0.04]' : ''}">
								<td class="px-4 py-2.5 whitespace-nowrap">
									<div class="flex items-center gap-1.5">
										{#if ti}<Icon icon={ti} class="w-4 h-4 {rankAccent(e.rank)}" />{/if}
										<span class="text-stone-200 font-semibold tabular-nums">{e.rank}</span>
										{#if e.delta > 0}
											<span class="text-up text-[0.65rem] tabular-nums inline-flex items-center"><Icon icon="mdi:menu-up" class="w-3 h-3" />{e.delta}</span>
										{:else if e.delta < 0}
											<span class="text-down text-[0.65rem] tabular-nums inline-flex items-center"><Icon icon="mdi:menu-down" class="w-3 h-3" />{-e.delta}</span>
										{/if}
									</div>
								</td>
								<td class="px-4 py-2.5 whitespace-nowrap">
									<div class="flex items-center gap-2.5">
										<span class="w-2 h-2 rounded-full shrink-0" style="background: {c};"></span>
										<a href="/profile/{e.username}" class="text-stone-200 truncate max-w-[200px] hover:text-amber-400 transition">{displayName(e)}</a>
									</div>
								</td>
								<td class="px-4 py-2.5 hidden sm:table-cell">
									<Sparkline data={e.spark ?? []} color={c} />
								</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-400 tabular-nums hidden md:table-cell">{e.challenges_solved}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-amber-500/90 font-semibold tabular-nums">{e.total_score.toLocaleString()}</td>
								<td class="px-4 py-2.5 whitespace-nowrap text-right text-stone-500 text-xs tabular-nums hidden xl:table-cell">{formatDate(e.last_solve_at)}</td>
								<td class="px-3 py-2.5 text-right">
									<button
										on:click={() => share(e)}
										title="Share rank card"
										class="text-stone-700 hover:text-amber-400 opacity-0 group-hover:opacity-100 focus:opacity-100 transition"
									>
										<Icon icon="mdi:share-variant-outline" class="w-4 h-4" />
									</button>
								</td>
							</tr>
						{/each}
						{#if filtered.length === 0}
							<tr><td colspan="7" class="px-4 py-8 text-center text-stone-500">No players match “{search}”.</td></tr>
						{/if}
					</tbody>
				</table>
			</div>
		</Card>
	{/if}
</div>
