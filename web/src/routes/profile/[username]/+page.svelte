<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { API_BASE } from '$lib/config';
	import { teamColor, categoryColor, rankAccent } from '$lib/rank';
	import { formatDur } from '$lib/chart/time';
	import Card from '$lib/components/Card.svelte';
	import StatTile from '$lib/components/StatTile.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import ProfileScoreChart from '$lib/components/ProfileScoreChart.svelte';
	import CategoryBars from '$lib/components/CategoryBars.svelte';
	import SolveTimeline from '$lib/components/SolveTimeline.svelte';

	interface Solve {
		name: string;
		slug: string;
		category?: string;
		points: number;
		solved_at: number;
	}

	$: username = $page.params.username ?? '';

	let loading = true;
	let error = '';
	let totalScore = 0;
	let solvedCount = 0;
	let rank: number | null = null;
	let solves: Solve[] = [];

	const catOf = (s: Solve) => s.category || 'Uncategorized';
	// Muted, consistent per-category color (see rank.ts) — never the vibrant API field.
	const catColor = (s: Solve) => categoryColor(s.category);

	$: sorted = [...solves].sort((a, b) => a.solved_at - b.solved_at);
	$: t0 = sorted.length ? sorted[0].solved_at : 0;
	$: lastAt = sorted.length ? sorted[sorted.length - 1].solved_at : 0;
	$: spanSec = lastAt - t0;

	// Stat-tile values, all derived from the solves data client-side.
	$: rankLabel = rank != null ? `#${rank}` : '—';
	$: spanLabel = spanSec > 0 ? formatDur(spanSec) : '—';
	$: firstDate = t0 ? new Date(t0 * 1000) : null;
	$: firstDateLabel = firstDate
		? firstDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
		: '—';
	$: firstTimeLabel = firstDate
		? firstDate.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
		: '';

	$: scorePoints = (() => {
		let cum = 0;
		return sorted.map((s) => {
			cum += s.points;
			return { x: s.solved_at - t0, y: cum, color: catColor(s), label: s.name };
		});
	})();

	$: categories = (() => {
		const map = new Map<
			string,
			{ name: string; color: string; total: number; segments: { points: number; label: string }[] }
		>();
		for (const s of solves) {
			const key = catOf(s);
			if (!map.has(key)) map.set(key, { name: key, color: catColor(s), total: 0, segments: [] });
			const c = map.get(key)!;
			c.total += s.points;
			c.segments.push({ points: s.points, label: s.name });
		}
		return [...map.values()].sort((a, b) => b.total - a.total);
	})();

	$: rowIndex = new Map(categories.map((c, i) => [c.name, i]));
	$: timelineRows = categories.map((c) => ({ name: c.name, color: c.color }));
	$: timelineSolves = solves.map((s) => ({
		row: rowIndex.get(catOf(s)) ?? 0,
		x: s.solved_at - t0,
		color: catColor(s),
		label: s.name
	}));

	$: listGroups = categories.map((c) => ({
		name: c.name,
		color: c.color,
		total: c.total,
		items: solves
			.filter((s) => catOf(s) === c.name)
			.sort((a, b) => a.solved_at - b.solved_at)
			.map((s) => ({ name: s.name, slug: s.slug, points: s.points, rel: s.solved_at - t0 }))
	}));


	async function load(name: string) {
		loading = true;
		error = '';
		try {
			const res = await fetch(`${API_BASE}/api/v1/profile/${encodeURIComponent(name)}`);
			if (res.status === 404) {
				error = 'Player not found';
				return;
			}
			if (!res.ok) throw new Error(`Failed to load profile (${res.status})`);
			const data = await res.json();
			solves = data.solves ?? [];
			totalScore = data.user?.total_score ?? 0;
			solvedCount = data.user?.challenges_solved ?? solves.length;

			const sbRes = await fetch(`${API_BASE}/api/v1/scoreboard`).catch(() => null);
			if (sbRes && sbRes.ok) {
				const sb = await sbRes.json();
				const me = (sb.leaderboard ?? []).find((e: { username: string }) => e.username === name);
				rank = me ? me.rank : null;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load profile';
		} finally {
			loading = false;
		}
	}

	let loaded = '';
	$: if (username && username !== loaded) {
		loaded = username;
		load(username);
	}

	onMount(() => {
		if (username && username !== loaded) {
			loaded = username;
			load(username);
		}
	});
</script>

<svelte:head>
	<title>{username} - Anvil</title>
</svelte:head>

<div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<a
		href="/scoreboard"
		class="inline-flex items-center gap-1.5 text-sm text-stone-500 hover:text-stone-300 transition mb-6"
	>
		<Icon icon="mdi:arrow-left" class="w-4 h-4" /> Scoreboard
	</a>

	{#if loading}
		<div class="flex items-center justify-center py-16">
			<Icon icon="mdi:loading" class="w-6 h-6 text-stone-500 animate-spin" />
		</div>
	{:else if error}
		<EmptyState icon="mdi:account-question" text={error}>
			<p class="text-stone-600 text-sm mt-1">
				No player named <span class="text-stone-400">{username}</span>.
			</p>
		</EmptyState>
	{:else}
		<!-- Identity -->
		<div class="mb-6">
			<div class="flex items-center gap-2.5">
				<span class="w-2.5 h-2.5 rounded-full shrink-0" style="background: {teamColor(username)};"></span>
				<h1 class="text-2xl font-bold text-stone-100 tracking-tight truncate">{username}</h1>
			</div>
			{#if rank != null}
				<div class="mt-1 inline-flex items-center gap-1.5 text-sm {rankAccent(rank)}">
					<Icon
						icon={rank === 1 ? 'mdi:trophy' : rank <= 3 ? 'mdi:medal' : 'mdi:pound'}
						class="w-3.5 h-3.5"
					/>
					<span class="font-medium tabular-nums">{rank}</span>
				</div>
			{/if}
		</div>

		{#if solves.length === 0}
			<EmptyState icon="mdi:flag-outline" text="No solves yet." />
		{:else}
			<!-- Stat tiles -->
			<div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 mb-6">
				<StatTile label="Rank" value={rankLabel} accent />
				<StatTile label="Points" value={totalScore.toLocaleString()} />
				<StatTile label="Solved" value={solvedCount} />
				<StatTile label="First solve" value={firstDateLabel} sub={firstTimeLabel} />
				<StatTile label="Active span" value={spanLabel} sub="first → last" />
			</div>

			<!-- Score over time -->
			<div class="mb-6">
				<Card title="Score over time">
					<span slot="meta" class="text-stone-500 text-xs">since first solve</span>
					<ProfileScoreChart points={scorePoints} />
				</Card>
			</div>

			<!-- Category breakdown + timeline -->
			<div class="grid lg:grid-cols-2 gap-6 mb-6">
				<Card title="Points by category">
					<CategoryBars {categories} />
				</Card>
				<Card title="Solve timeline">
					<SolveTimeline rows={timelineRows} solves={timelineSolves} />
				</Card>
			</div>

			<!-- Solved challenges -->
			<Card title="Solved">
				<span slot="meta" class="text-stone-500 text-xs tabular-nums">{solves.length}</span>
				<div class="grid sm:grid-cols-2 gap-x-6 gap-y-5">
					{#each listGroups as g}
						<div>
							<div class="flex items-center gap-2 pb-2 mb-1 border-b border-stone-800">
								<span class="w-2 h-2 rounded-full shrink-0" style="background: {g.color};"></span>
								<span class="text-xs font-medium uppercase tracking-wide text-stone-300 truncate"
									>{g.name}</span
								>
								<span class="ml-auto text-xs text-stone-500 tabular-nums">{g.total} pts</span>
							</div>
							<ul class="divide-y divide-stone-800/60">
								{#each g.items as it}
									<li class="flex items-center gap-2 py-1.5 text-sm">
										<Icon icon="mdi:check" class="w-3.5 h-3.5 text-stone-600 shrink-0" />
										<a
											href="/challenges/{it.slug}"
											class="text-stone-300 hover:text-stone-100 transition truncate">{it.name}</a
										>
										<span class="ml-auto flex items-center gap-3 shrink-0 tabular-nums">
											<span class="text-stone-600 text-xs">+{formatDur(it.rel)}</span>
											<span class="text-stone-300 text-xs font-medium w-12 text-right">{it.points}</span>
										</span>
									</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			</Card>
		{/if}
	{/if}
</div>
