<script lang="ts">
	export let rank = 0;
</script>

{#if Number.isInteger(rank) && rank > 0}
	<span
		class="inline-flex h-5 min-w-5 shrink-0 items-center justify-center whitespace-nowrap rounded-full border border-amber-500/25 bg-amber-500/[0.07] px-1.5 text-[0.62rem] font-semibold leading-none tabular-nums text-amber-500"
		title={`Scoreboard rank #${rank}`}
	>
		<span class="optical-label">#{rank}</span>
	</span>
{/if}
