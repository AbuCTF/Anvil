<script lang="ts">
	// A tertiary-gray caption above a large mono value. See DESIGN.md.
	export let label: string;
	export let value: string | number;
	export let sub = '';
	export let accent = false;
</script>

<div class="bg-stone-900/40 border border-stone-800 rounded-lg px-4 py-3">
	<div class="text-[0.7rem] uppercase tracking-wider text-stone-500">{label}</div>
	<div class="mt-1 text-xl font-semibold tabular-nums {accent ? 'text-amber-500' : 'text-stone-100'}">{value}</div>
	{#if sub}<div class="text-xs text-stone-600 mt-0.5 tabular-nums">{sub}</div>{/if}
</div>
