<script lang="ts">
	import { stepPath } from '$lib/chart/path';
	import { linear, niceMax } from '$lib/chart/scale';
	import { formatDur, timeTicks } from '$lib/chart/time';

	// Points are cumulative: x = seconds since first solve, y = running total.
	export let points: { x: number; y: number; color: string; label: string }[] = [];
	export let height = 260;

	let width = 640;
	const padL = 44;
	const padR = 14;
	const padT = 14;
	const padB = 24;

	$: xMax = points.length ? Math.max(...points.map((p) => p.x)) : 1;
	$: yMax = niceMax(points.length ? Math.max(...points.map((p) => p.y)) : 1);
	$: sx = linear(0, xMax === 0 ? 1 : xMax, padL, width - padR);
	$: sy = linear(0, yMax, height - padB, padT);
	$: screen = points.map((p) => ({ ...p, cx: sx(p.x), cy: sy(p.y) }));
	$: line = stepPath(screen.map((p) => ({ x: p.cx, y: p.cy })));
	$: area = screen.length
		? `${stepPath(screen.map((p) => ({ x: p.cx, y: p.cy })))} L ${sx(xMax)} ${sy(0)} L ${padL} ${sy(0)} Z`
		: '';
	$: yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(f * yMax));
	$: xTicks = timeTicks(xMax);
</script>

<div class="w-full" bind:clientWidth={width}>
	<svg {width} {height} viewBox="0 0 {width} {height}" class="block">
		<defs>
			<linearGradient id="scoreFill" x1="0" x2="0" y1="0" y2="1">
				<stop offset="0%" stop-color="#f59e0b" stop-opacity="0.22" />
				<stop offset="100%" stop-color="#f59e0b" stop-opacity="0" />
			</linearGradient>
		</defs>

		{#each yTicks as t}
			<line x1={padL} x2={width - padR} y1={sy(t)} y2={sy(t)} class="stroke-stone-800" stroke-width="1" />
			<text x={padL - 8} y={sy(t) + 3} text-anchor="end" class="fill-stone-500 text-[10px] tabular-nums">{t}</text>
		{/each}
		{#each xTicks as t}
			<text x={sx(t)} y={height - 6} text-anchor="middle" class="fill-stone-600 text-[10px] tabular-nums">
				{t === 0 ? '0' : `+${formatDur(t)}`}
			</text>
		{/each}

		{#if area}
			<path d={area} fill="url(#scoreFill)" stroke="none" />
			<path d={line} fill="none" stroke="#f59e0b" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
		{/if}

		{#each screen as p}
			<circle cx={p.cx} cy={p.cy} r="3.5" fill={p.color} stroke="#0c0a09" stroke-width="1.5">
				<title>{p.label} · +{formatDur(p.x)} · {p.y} pts</title>
			</circle>
		{/each}
	</svg>
</div>
