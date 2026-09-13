<script lang="ts">
	export let rank = 0;
</script>

{#if Number.isInteger(rank) && rank > 0}
	<span
		class="rank-badge inline-grid h-5 min-w-5 shrink-0 place-items-center whitespace-nowrap rounded-full border border-amber-500/25 bg-amber-500/[0.07] px-1.5 text-[0.62rem] font-semibold leading-none tabular-nums text-amber-500"
		title={`Scoreboard rank #${rank}`}
	>
		<span class="rank-badge-label">#{rank}</span>
	</span>
{/if}

<style>
	.rank-badge {
		font-family: 'Inter Variable', 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
	}

	.rank-badge-label {
		line-height: 1;
		transform: translateY(1.25px);
	}
</style>
