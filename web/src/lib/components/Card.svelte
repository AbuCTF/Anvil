<script lang="ts">
	// Surface panel. See DESIGN.md. Optional header (title + right-aligned meta slot).
	export let title = '';
	export let bodyClass = 'p-4';
	export let hasHeader = true;
</script>

<div class="bg-stone-900/40 border border-stone-800 rounded-lg overflow-hidden">
	{#if hasHeader && (title || $$slots.header || $$slots.meta)}
		<div class="px-4 py-3 border-b border-stone-800 flex items-center gap-2">
			<slot name="header">
				<h2 class="text-sm font-semibold text-stone-200 uppercase tracking-wide">{title}</h2>
			</slot>
			<div class="ml-auto flex items-center gap-2"><slot name="meta" /></div>
		</div>
	{/if}
	<div class={bodyClass}>
		<slot />
	</div>
</div>
