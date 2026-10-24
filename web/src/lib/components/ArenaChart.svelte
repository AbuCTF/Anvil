<script context="module" lang="ts">
	export interface HistorySeries {
		team_id: string;
		team: string;
		points: { x: number; y: number }[];
	}
</script>

<script lang="ts">
	// koth points / score over time card, fed by /arena/state history + koth_history
	import LineChart from '$lib/components/LineChart.svelte';
	import { teamColor } from '$lib/rank';
	import type { Series } from '$lib/chart/path';

	export let history: HistorySeries[] = [];
	export let kothHistory: HistorySeries[] = [];
	// leading team name, emphasized on the score lines
	export let leader: string | null = null;
	export let className = 'mb-6';

	let chartMode: 'koth' | 'score' = 'koth';

	const toSeries = (h: HistorySeries): Series => ({ label: h.team, color: teamColor(h.team_id), points: h.points });
	$: raceSeries = history.map(toSeries);
	// koth lines start from 0 one round before the first close
	$: kothSeries = kothHistory.map((h) => {
		const p = h.points;
		return toSeries({ ...h, points: p.length ? [{ x: p[0].x - 1, y: 0 }, ...p] : [] });
	});
	$: leaderIdx = leader ? raceSeries.findIndex((s) => s.label === leader) : -1;
	const lastY = (s: Series) => s.points[s.points.length - 1]?.y ?? 0;
	$: kothTop = Math.max(0, ...kothSeries.map(lastY));
	$: kothLeadIdx = kothTop > 0 ? kothSeries.findIndex((s) => lastY(s) === kothTop) : -1;
	$: chartKoth = chartMode === 'koth';
	$: chartSeries = chartKoth ? kothSeries : raceSeries;
	// legend: top 8 by whatever the chart shows now
	$: ranked = chartSeries.filter((s) => s.points.length).sort((a, b) => lastY(b) - lastY(a));
	$: legend = ranked.slice(0, 8);
</script>

{#if raceSeries.length || kothSeries.length}
	<div class="bg-stone-900/40 rounded-lg border border-stone-800 overflow-hidden {className}">
		<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between gap-3">
			<h2 class="min-w-0 truncate text-[0.95rem] font-semibold text-stone-200">
				{#if chartKoth}<span class="sm:hidden">KotH points</span><span class="hidden sm:inline">King of the Hill points</span>{:else}Score over time{/if}
			</h2>
			<div class="flex overflow-hidden rounded-md border border-stone-800 text-xs shrink-0">
				{#each [['koth', 'KotH points'], ['score', 'Score']] as [mode, label], i}
					<button
						type="button"
						class="px-2.5 py-1 transition-colors {i ? 'border-l border-stone-800' : ''} {chartMode === mode
							? 'bg-stone-800 text-stone-200'
							: 'text-stone-500 hover:text-stone-300'}"
						aria-pressed={chartMode === mode}
						on:click={() => (chartMode = mode === 'koth' ? 'koth' : 'score')}>{label}</button
					>
				{/each}
			</div>
		</div>
		<div class="p-4">
			{#if !chartSeries.some((s) => s.points.length)}
				<p class="py-10 text-center text-xs text-stone-500">{chartKoth ? 'No closed rounds yet.' : 'No scores yet.'}</p>
			{:else}
				<LineChart
					series={chartSeries}
					height={280}
					curve="step"
					emphasize={chartKoth ? kothLeadIdx : leaderIdx}
					ariaLabel={chartKoth ? 'King of the Hill points' : 'Score over time'}
					xFormat={(x) => (chartKoth ? 'r' : 't') + Math.round(x)}
					integerX
				/>
				<div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] leading-none text-stone-500">
					{#each legend as s}
						<span class="inline-flex items-center gap-1">
							<span class="h-1.5 w-1.5 shrink-0 rounded-full" style="background: {s.color};"></span>
							<span class="optical-label max-w-32 truncate" title={s.label}>{s.label}</span>
						</span>
					{/each}
					{#if ranked.length > legend.length}<span class="optical-label text-stone-600">+{ranked.length - legend.length} more</span>{/if}
				</div>
			{/if}
		</div>
	</div>
{/if}
