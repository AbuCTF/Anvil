<script lang="ts">
	import { stepPath, monotonePath, type Pt } from '$lib/chart/path';
	import { linear } from '$lib/chart/scale';

	export let data: number[] = [];
	export let width = 104;
	export let height = 28;
	export let color = 'currentColor';
	export let curve: 'step' | 'monotone' = 'step';
	export let area = true;

	const pad = 3;

	$: pts = (() => {
		if (data.length < 2) return [] as Pt[];
		const min = Math.min(...data);
		const max = Math.max(...data);
		const x = linear(0, data.length - 1, pad, width - pad);
		const y = linear(min, max === min ? min + 1 : max, height - pad, pad);
		return data.map((v, i) => ({ x: x(i), y: y(v) }));
	})();
	$: d = pts.length > 1 ? (curve === 'step' ? stepPath(pts) : monotonePath(pts)) : '';
	$: areaD = d ? `${d} L ${pts[pts.length - 1].x} ${height - pad} L ${pts[0].x} ${height - pad} Z` : '';
	$: last = pts[pts.length - 1];
</script>

{#if pts.length > 1}
	<svg {width} {height} viewBox="0 0 {width} {height}" class="block overflow-visible">
		{#if area}<path d={areaD} fill={color} fill-opacity="0.1" stroke="none" />{/if}
		<path {d} fill="none" stroke={color} stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" />
		<circle cx={last.x} cy={last.y} r="1.6" fill={color} />
	</svg>
{/if}
