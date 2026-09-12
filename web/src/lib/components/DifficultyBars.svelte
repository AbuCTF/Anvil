<script lang="ts">
	export let rows: { name: string; solved: number; total: number; color: string }[] = [];
</script>

<div class="space-y-3">
	{#each rows as row}
		<div>
			<div class="mb-1.5 flex items-center gap-2 text-xs leading-none">
				<span class="w-2 h-2 shrink-0 rounded-full" style="background: {row.color};"></span>
				<span class="optical-label capitalize text-stone-300">{row.name}</span>
				<span class="optical-label ml-auto tabular-nums text-stone-500">{row.solved}/{row.total}</span>
			</div>
			<div class="h-2 overflow-hidden rounded-full bg-stone-800/70">
				<div
					class="h-full rounded-full transition-[width] duration-300"
					style="width: {row.total ? (row.solved / row.total) * 100 : 0}%; background: {row.color}; opacity: 0.78;"
				></div>
			</div>
		</div>
	{/each}
</div>
