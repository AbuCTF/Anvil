<script lang="ts">
	import { monotonePath, stepPath, type Series } from '$lib/chart/path';
	import { linear, niceMax } from '$lib/chart/scale';

	export let series: Series[] = [];
	export let height = 320;
	export let curve: 'step' | 'monotone' = 'step';
	// Optional x-axis tick formatter (e.g. clock time or tick number). Null = no x labels.
	export let xFormat: ((x: number) => string) | null = null;
	// Index of the series to emphasise (leader); others are drawn dimmer.
	export let emphasize = -1;

	let width = 800;
	const padL = 40;
	const padR = 12;
	const padT = 12;
	$: padB = xFormat ? 26 : 20;

	const draw = (pts: { x: number; y: number }[]) => (curve === 'step' ? stepPath(pts) : monotonePath(pts));

	$: allX = series.flatMap((s) => s.points.map((p) => p.x));
	$: allY = series.flatMap((s) => s.points.map((p) => p.y));
	$: xMin = allX.length ? Math.min(...allX) : 0;
	$: xMax = allX.length ? Math.max(...allX) : 1;
	$: yMax = niceMax(allY.length ? Math.max(...allY) : 1);
	$: sx = linear(xMin, xMax === xMin ? xMin + 1 : xMax, padL, width - padR);
	$: sy = linear(0, yMax, height - padB, padT);
	$: paths = series.map((s, i) => ({
		color: s.color,
		label: s.label,
		d: draw(s.points.map((p) => ({ x: sx(p.x), y: sy(p.y) }))),
		dim: emphasize >= 0 && i !== emphasize
	}));
	$: yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(f * yMax));
	$: xTicks = xFormat ? [0, 0.25, 0.5, 0.75, 1].map((f) => xMin + f * (xMax - xMin)) : [];
</script>

<div class="w-full" bind:clientWidth={width}>
	<svg {width} {height} viewBox="0 0 {width} {height}" class="block">
		{#each yTicks as t}
			<line x1={padL} x2={width - padR} y1={sy(t)} y2={sy(t)} class="stroke-stone-800/60" stroke-width="1" />
			<text x={padL - 6} y={sy(t) + 3} text-anchor="end" class="fill-stone-600 text-[10px] tabular-nums">{t}</text>
		{/each}
		{#each xTicks as t}
			<text x={sx(t)} y={height - 6} text-anchor="middle" class="fill-stone-600 text-[10px] tabular-nums">
				{xFormat ? xFormat(t) : ''}
			</text>
		{/each}
		{#each paths as p}
			<path
				d={p.d}
				fill="none"
				stroke={p.color}
				stroke-width={p.dim ? 1.25 : 1.75}
				stroke-opacity={p.dim ? 0.5 : 1}
				stroke-linecap="round"
				stroke-linejoin="round"
			/>
		{/each}
	</svg>
</div>
