<script lang="ts">
	// One bar per category, segmented per solve. Widths scale to the top category.
	export let categories: {
		name: string;
		color: string;
		total: number;
		segments: { points: number; label: string }[];
	}[] = [];

	$: max = categories.length ? Math.max(...categories.map((c) => c.total)) : 1;
</script>

<div class="space-y-2.5">
	{#each categories as c}
		<div class="flex items-center gap-3">
			<div class="w-24 shrink-0 truncate text-right text-xs text-stone-400" title={c.name}>{c.name}</div>
			<div class="flex-1">
				<div
					class="flex h-4 items-stretch gap-px overflow-hidden rounded"
					style="width: {(c.total / max) * 100}%"
				>
					{#each c.segments as seg}
						<div
							class="h-full first:rounded-l last:rounded-r"
							style="flex: {seg.points}; background: {c.color};"
							title="{seg.label} · {seg.points} pts"
						></div>
					{/each}
				</div>
			</div>
			<div class="w-14 shrink-0 text-right text-xs font-semibold tabular-nums text-stone-200">{c.total}</div>
		</div>
	{/each}
</div>
