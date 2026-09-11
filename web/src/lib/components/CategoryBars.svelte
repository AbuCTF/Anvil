<script lang="ts">
	// One bar per category, segmented per solve. Bar length scales to the top category.
	export let categories: {
		name: string;
		color: string;
		total: number;
		segments: { points: number; label: string }[];
	}[] = [];

	$: max = categories.length ? Math.max(...categories.map((c) => c.total)) : 1;
</script>

<div class="space-y-3">
	{#each categories as c}
		<div>
			<div class="flex items-center gap-2 mb-1.5 text-xs leading-none">
				<span class="w-2 h-2 rounded-full shrink-0" style="background: {c.color};"></span>
				<span class="text-stone-300 truncate" title={c.name}>{c.name}</span>
				<span class="text-stone-600 tabular-nums">· {c.segments.length}</span>
				<span class="ml-auto font-semibold tabular-nums text-stone-200">{c.total}</span>
			</div>
			<div
				class="flex h-2 items-stretch gap-px overflow-hidden rounded-full"
				style="width: {(c.total / max) * 100}%"
			>
				{#each c.segments as seg}
					<div
						class="h-full first:rounded-l-full last:rounded-r-full"
						style="flex: {seg.points}; background: {c.color};"
						title="{seg.label} · {seg.points} pts"
					></div>
				{/each}
			</div>
		</div>
	{/each}
</div>
