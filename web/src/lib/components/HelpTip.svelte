<script lang="ts">
	import Icon from '@iconify/svelte';

	export let text: string;
	export let label = 'More information';
	export let align: 'left' | 'right' = 'left';
</script>

<span class="group relative inline-flex align-middle">
	<button type="button" aria-label={label} class="inline-grid h-5 w-5 place-items-center rounded text-stone-600 transition-colors hover:bg-stone-800 hover:text-stone-300 focus:bg-stone-800 focus:text-stone-300 focus:outline-none">
		<Icon icon="mdi:information-outline" class="h-3.5 w-3.5" />
	</button>
	<span role="tooltip" class="pointer-events-none invisible absolute top-7 z-[80] w-64 rounded-md border border-stone-700 bg-stone-950 px-3 py-2 text-left text-[11px] font-normal normal-case leading-relaxed tracking-normal text-stone-300 opacity-0 shadow-xl transition-opacity group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100 {align === 'right' ? 'right-0' : 'left-0'}">
		{text}
	</span>
</span>
