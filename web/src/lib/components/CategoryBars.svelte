<script lang="ts">
	// one bar per category, segmented per solve. bar length scales to the top category.
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
				<span class="optical-label text-stone-300 truncate" title={c.name}>{c.name}</span>
				<span class="optical-label text-stone-600 tabular-nums">· {c.segments.length}</span>
				<span class="optical-label ml-auto font-semibold tabular-nums text-stone-200">{c.total}</span>
			</div>
			<div class="h-2 overflow-hidden rounded-full bg-stone-800/70">
				<div
					class="flex h-full items-stretch gap-px overflow-hidden rounded-full"
					style="width: {(c.total / max) * 100}%"
				>
					{#each c.segments as seg, i}
						<div
							class="h-full min-w-px first:rounded-l-full last:rounded-r-full"
							style="flex: {seg.points}; background: {c.color}; opacity: {0.52 + (i % 4) * 0.14};"
							title="{seg.label} · {seg.points} pts"
						></div>
					{/each}
				</div>
			</div>
		</div>
	{/each}
</div>
