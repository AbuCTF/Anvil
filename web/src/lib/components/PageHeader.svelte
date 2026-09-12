<script lang="ts">
	// Page title + subtitle, with an optional right-aligned actions slot. See DESIGN.md.
	export let title: string;
	export let subtitle = '';
	export let compact = false;
</script>

<div class="flex flex-wrap items-end justify-between gap-4 {compact ? 'mb-4' : 'mb-6'}">
	<div>
		<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">{title}</h1>
		{#if subtitle}<p class="text-stone-500 text-sm mt-0.5">{subtitle}</p>{/if}
	</div>
	<slot name="actions" />
</div>
