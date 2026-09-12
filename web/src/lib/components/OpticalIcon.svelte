<script lang="ts">
	import Icon from '@iconify/svelte';

	export let icon: string;
	export let size = 14;
	export let box = 16;
	export let className = '';
	export let color: string | undefined = undefined;
</script>

<span
	aria-hidden="true"
	class="inline-flex shrink-0 items-center justify-center {className}"
	style="width: {box}px; height: {box}px;"
	style:color
>
	<Icon
		{icon}
		class="block shrink-0"
		style="width: {size}px; height: {size}px;"
	/>
</span>
