<script lang="ts">
	import Icon from '@iconify/svelte';
	// terse, mono empty state. no illustrations. see DESIGN.md.
	export let icon = 'mdi:inbox-outline';
	export let text = 'Nothing here yet.';
</script>

<div class="flex flex-col items-center justify-center py-12 text-center">
	<Icon {icon} class="w-10 h-10 text-stone-700 mb-3" />
	<p class="text-stone-500 text-sm">{text}</p>
	<slot />
</div>
