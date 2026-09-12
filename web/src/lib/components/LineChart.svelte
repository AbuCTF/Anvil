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

	// Hover crosshair: read each series' value at the cursor's time.
	let hoverX: number | null = null;
	$: dataX =
		hoverX == null ? null : xMin + ((hoverX - padL) / Math.max(1, width - padR - padL)) * (xMax - xMin);
	$: hoverRows =
		dataX == null
			? []
			: series
					.map((s) => {
						let v: number | null = null;
						for (const p of s.points) if (p.x <= (dataX as number)) v = p.y;
						return { label: s.label, color: s.color, value: v, cy: v == null ? null : sy(v) };
					})
					.filter((r) => r.value != null)
					.sort((a, b) => (b.value as number) - (a.value as number));
	$: tipLeft = hoverX == null ? 0 : Math.min(Math.max(0, hoverX + 12), Math.max(0, width - 172));

	function onMove(e: MouseEvent) {
		const rect = (e.currentTarget as SVGElement).getBoundingClientRect();
		hoverX = Math.max(padL, Math.min(width - padR, e.clientX - rect.left));
	}
</script>

<div class="relative w-full" bind:clientWidth={width}>
	<svg
		{width}
		{height}
		viewBox="0 0 {width} {height}"
		class="block"
		role="presentation"
		on:mousemove={onMove}
		on:mouseleave={() => (hoverX = null)}
	>
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

		{#if hoverX != null}
			<line x1={hoverX} x2={hoverX} y1={padT} y2={height - padB} class="stroke-stone-600" stroke-width="1" stroke-dasharray="3 3" />
			{#each hoverRows as r}
				{#if r.cy != null}
					<circle cx={hoverX} cy={r.cy} r="3" fill={r.color} stroke="#0c0a09" stroke-width="1.5" />
				{/if}
			{/each}
		{/if}
	</svg>

	{#if hoverX != null && hoverRows.length}
		<div
			class="pointer-events-none absolute top-2 z-10 w-[160px] rounded-md border border-stone-700 bg-stone-950/95 px-2.5 py-1.5 text-xs shadow-lg"
			style="left: {tipLeft}px;"
		>
			{#if xFormat && dataX != null}<div class="text-stone-500 mb-1 tabular-nums">{xFormat(dataX)}</div>{/if}
			{#each hoverRows.slice(0, 8) as r}
				<div class="flex items-center gap-1.5 leading-none">
					<span class="w-2 h-2 rounded-full shrink-0" style="background: {r.color};"></span>
					<span class="optical-label text-stone-300 truncate">{r.label}</span>
					<span class="optical-label ml-auto text-stone-400 tabular-nums">{Math.round(r.value ?? 0)}</span>
				</div>
			{/each}
			{#if hoverRows.length > 8}<div class="text-stone-600 mt-0.5">+{hoverRows.length - 8} more</div>{/if}
		</div>
	{/if}
</div>
