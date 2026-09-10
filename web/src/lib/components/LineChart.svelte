<script lang="ts">
	import { monotonePath, type Series } from '$lib/chart/path';
	import { linear, niceMax } from '$lib/chart/scale';

	export let series: Series[] = [];
	export let height = 320;

	let width = 800;
	const padL = 44;
	const padR = 12;
	const padT = 12;
	const padB = 22;

	$: allX = series.flatMap((s) => s.points.map((p) => p.x));
	$: allY = series.flatMap((s) => s.points.map((p) => p.y));
	$: xMin = allX.length ? Math.min(...allX) : 0;
	$: xMax = allX.length ? Math.max(...allX) : 1;
	$: yMax = niceMax(allY.length ? Math.max(...allY) : 1);
	$: sx = linear(xMin, xMax === xMin ? xMin + 1 : xMax, padL, width - padR);
	$: sy = linear(0, yMax, height - padB, padT);
	$: paths = series.map((s) => ({
		color: s.color,
		label: s.label,
		d: monotonePath(s.points.map((p) => ({ x: sx(p.x), y: sy(p.y) })))
	}));
	$: yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(f * yMax));
</script>

<div class="w-full" bind:clientWidth={width}>
	<svg {width} {height} viewBox="0 0 {width} {height}" class="block">
		{#each yTicks as t}
			<line
				x1={padL}
				x2={width - padR}
				y1={sy(t)}
				y2={sy(t)}
				class="stroke-stone-800"
				stroke-width="1"
			/>
			<text x={padL - 8} y={sy(t) + 3} text-anchor="end" class="fill-stone-500 text-[10px] tabular-nums">
				{t}
			</text>
		{/each}
		{#each paths as p}
			<path
				d={p.d}
				fill="none"
				stroke={p.color}
				stroke-width="2"
				stroke-linecap="round"
				stroke-linejoin="round"
			/>
		{/each}
	</svg>
</div>
