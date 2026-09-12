<script lang="ts">
	import { linear } from '$lib/chart/scale';
	import { formatDur, timeTicks } from '$lib/chart/time';

	// One lane per category; a dot per solve at its time (seconds since first solve).
	export let rows: { name: string; color: string }[] = [];
	export let solves: { row: number; x: number; color: string; label: string }[] = [];

	let width = 640;
	const padL = 92;
	const padR = 14;
	const padT = 8;
	const padB = 22;
	const rowH = 24;

	$: height = padT + rows.length * rowH + padB;
	$: xMax = solves.length ? Math.max(...solves.map((s) => s.x)) : 1;
	$: sx = linear(0, xMax === 0 ? 1 : xMax, padL, width - padR);
	$: laneY = (i: number) => padT + i * rowH + rowH / 2;
	$: xTicks = timeTicks(xMax, width < 480 ? 2 : 4);
</script>

<div class="w-full" bind:clientWidth={width}>
	<svg {width} {height} viewBox="0 0 {width} {height}" class="block w-full" role="img" aria-label="Solve timeline by category">
		{#each rows as r, i}
			<line x1={padL} x2={width - padR} y1={laneY(i)} y2={laneY(i)} class="stroke-stone-800/70" stroke-width="1" />
			<text x={padL - 8} y={laneY(i) + 3} text-anchor="end" class="fill-stone-400 text-[10px]">
				{r.name.length > 12 ? r.name.slice(0, 11) + '…' : r.name}
			</text>
			<circle cx={padL - 2} cy={laneY(i)} r="2.5" fill={r.color} />
		{/each}
		{#each xTicks as t, index}
			<text
				x={sx(t)}
				y={height - 6}
				text-anchor={index === 0 ? 'start' : index === xTicks.length - 1 ? 'end' : 'middle'}
				class="fill-stone-600 text-[10px] tabular-nums"
			>
				{t === 0 ? '0' : `+${formatDur(t)}`}
			</text>
		{/each}
		{#each solves as s}
			<circle cx={sx(s.x)} cy={laneY(s.row)} r="3.5" fill={s.color} stroke="#0c0a09" stroke-width="1.5">
				<title>{s.label} · +{formatDur(s.x)}</title>
			</circle>
		{/each}
	</svg>
</div>
