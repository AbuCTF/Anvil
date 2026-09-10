<script lang="ts">
	import { monotonePath, type Pt } from '$lib/chart/path';
	import { linear } from '$lib/chart/scale';

	export let data: number[] = [];
	export let width = 96;
	export let height = 28;
	export let color = 'currentColor';
	export let strokeWidth = 1.5;

	const pad = 2;

	function toPoints(vals: number[]): Pt[] {
		if (vals.length < 2) return [];
		const min = Math.min(...vals);
		const max = Math.max(...vals);
		const x = linear(0, vals.length - 1, pad, width - pad);
		const y = linear(min, max === min ? min + 1 : max, height - pad, pad);
		return vals.map((v, i) => ({ x: x(i), y: y(v) }));
	}

	$: pts = toPoints(data);
	$: d = monotonePath(pts);
</script>

{#if pts.length > 1}
	<svg
		{width}
		{height}
		viewBox="0 0 {width} {height}"
		preserveAspectRatio="none"
		class="overflow-visible"
	>
		<path
			{d}
			fill="none"
			stroke={color}
			stroke-width={strokeWidth}
			vector-effect="non-scaling-stroke"
			stroke-linecap="round"
			stroke-linejoin="round"
		/>
	</svg>
{/if}
