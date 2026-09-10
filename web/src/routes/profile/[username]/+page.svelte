<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { API_BASE } from '$lib/config';
	import { teamColor, rankAccent } from '$lib/rank';
	import { formatDur } from '$lib/chart/time';
	import ProfileScoreChart from '$lib/components/ProfileScoreChart.svelte';
	import CategoryBars from '$lib/components/CategoryBars.svelte';
	import SolveTimeline from '$lib/components/SolveTimeline.svelte';

	interface Solve {
		name: string;
		slug: string;
		category?: string;
		category_color?: string;
		points: number;
		solved_at: number;
	}

	const FALLBACK = '#94a3b8';

	$: username = $page.params.username ?? '';

	let loading = true;
	let error = '';
	let totalScore = 0;
	let solvedCount = 0;
	let rank: number | null = null;
	let solves: Solve[] = [];

	const catOf = (s: Solve) => s.category || 'Uncategorized';
	const catColor = (s: Solve) => s.category_color || FALLBACK;

	$: sorted = [...solves].sort((a, b) => a.solved_at - b.solved_at);
	$: t0 = sorted.length ? sorted[0].solved_at : 0;

	$: scorePoints = (() => {
		let cum = 0;
		return sorted.map((s) => {
			cum += s.points;
			return { x: s.solved_at - t0, y: cum, color: catColor(s), label: s.name };
		});
	})();

	$: categories = (() => {
		const map = new Map<string, { name: string; color: string; total: number; segments: { points: number; label: string }[] }>();
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

	$: initials = username ? username.slice(0, 2).toUpperCase() : '?';

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
	<a href="/scoreboard" class="inline-flex items-center gap-1.5 text-sm text-stone-500 hover:text-amber-400 transition mb-6">
		<Icon icon="mdi:arrow-left" class="w-4 h-4" /> Scoreboard
	</a>

	{#if loading}
		<div class="flex items-center justify-center py-16">
			<Icon icon="mdi:loading" class="w-8 h-8 text-amber-500 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-12 text-center">
			<Icon icon="mdi:account-question" class="w-16 h-16 text-stone-600 mx-auto mb-4" />
			<h3 class="text-xl font-medium text-white mb-1">{error}</h3>
			<p class="text-stone-500">No player named <span class="text-stone-300">{username}</span>.</p>
		</div>
	{:else}
		<!-- Header -->
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-6 mb-6 flex flex-wrap items-center gap-5">
			<div
				class="w-16 h-16 rounded-lg flex items-center justify-center text-xl font-bold text-black shrink-0"
				style="background: {teamColor(username)};"
			>
				{initials}
			</div>
			<div class="min-w-0 flex-1">
				<h1 class="text-2xl font-bold text-white truncate">{username}</h1>
				<div class="mt-1 flex items-center gap-3 text-sm">
					{#if rank}
						<span class="inline-flex items-center gap-1.5 {rankAccent(rank)}">
							<Icon icon={rank === 1 ? 'mdi:trophy' : rank <= 3 ? 'mdi:medal' : 'mdi:pound'} class="w-4 h-4" />
							<span class="font-semibold tabular-nums">{rank}</span>
						</span>
						<span class="text-stone-700">·</span>
					{/if}
					<span class="text-stone-400 tabular-nums">{solvedCount} solved</span>
				</div>
			</div>
			<div class="text-right">
				<div class="text-3xl font-bold text-amber-500 tabular-nums">{totalScore.toLocaleString()}</div>
				<div class="text-xs text-stone-500">points</div>
			</div>
		</div>

		{#if solves.length === 0}
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-12 text-center">
				<Icon icon="mdi:flag-outline" class="w-14 h-14 text-stone-600 mx-auto mb-3" />
				<p class="text-stone-400">No solves yet.</p>
			</div>
		{:else}
			<!-- Score over time -->
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden mb-6">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:chart-line" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Score over time</h2>
					<span class="text-stone-500 text-sm ml-auto">since first solve</span>
				</div>
				<div class="p-4">
					<ProfileScoreChart points={scorePoints} />
				</div>
			</div>

			<!-- Category breakdown + timeline -->
			<div class="grid lg:grid-cols-2 gap-6 mb-6">
				<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
					<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
						<Icon icon="mdi:chart-bar" class="w-5 h-5 text-amber-500" />
						<h2 class="text-lg font-semibold text-white">Points by category</h2>
					</div>
					<div class="p-5">
						<CategoryBars {categories} />
					</div>
				</div>

				<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
					<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
						<Icon icon="mdi:timeline-clock" class="w-5 h-5 text-amber-500" />
						<h2 class="text-lg font-semibold text-white">Solve timeline</h2>
					</div>
					<div class="p-4">
						<SolveTimeline rows={timelineRows} solves={timelineSolves} />
					</div>
				</div>
			</div>

			<!-- Solved challenges -->
			<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
				<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
					<Icon icon="mdi:check-decagram" class="w-5 h-5 text-amber-500" />
					<h2 class="text-lg font-semibold text-white">Solved</h2>
					<span class="text-stone-500 text-sm ml-auto tabular-nums">{solves.length}</span>
				</div>
				<div class="p-5 grid sm:grid-cols-2 gap-x-6 gap-y-5">
					{#each listGroups as g}
						<div>
							<div class="flex items-center gap-2 pb-2 mb-1 border-b border-stone-800">
								<span class="w-2.5 h-2.5 rounded-full shrink-0" style="background: {g.color};"></span>
								<span class="text-sm font-semibold text-stone-200 truncate">{g.name}</span>
								<span class="text-xs text-stone-500 tabular-nums ml-auto">{g.total} pts</span>
							</div>
							<ul class="divide-y divide-stone-800/60">
								{#each g.items as it}
									<li class="flex items-center gap-2 py-1.5 text-sm">
										<Icon icon="mdi:check-circle" class="w-4 h-4 text-green-500 shrink-0" />
										<a href="/challenges/{it.slug}" class="text-stone-300 hover:text-amber-400 transition truncate">{it.name}</a>
										<span class="ml-auto flex items-center gap-3 shrink-0">
											<span class="text-stone-600 text-xs tabular-nums">+{formatDur(it.rel)}</span>
											<span class="text-amber-500/90 text-xs font-medium tabular-nums w-12 text-right">{it.points}</span>
										</span>
									</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			</div>
		{/if}
	{/if}
</div>
