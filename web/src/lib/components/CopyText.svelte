<script lang="ts">
	import Icon from '@iconify/svelte';

	export let text: string;
	export let label = '';
	export let className = '';
	export let quiet = false;
	export let wrap = false;

	let copied = false;
	let timer: ReturnType<typeof setTimeout>;

	async function copy() {
		try {
			await navigator.clipboard.writeText(text);
			copied = true;
			clearTimeout(timer);
			timer = setTimeout(() => (copied = false), 1500);
		} catch {
			// clipboard blocked, text stays selectable
		}
	}
</script>

<button
	type="button"
	on:click={copy}
	title={copied ? 'Copied' : `Copy ${text}`}
	class="group inline-flex max-w-full items-center gap-1.5 rounded font-mono focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-stone-500 {className}"
>
	<span class="text-left {wrap ? 'break-words' : 'truncate'}">{label || text}</span>
	<Icon icon={copied ? 'mdi:check' : 'mdi:content-copy'} class="h-3.5 w-3.5 shrink-0 {copied ? 'text-[#9bcea8]' : `text-stone-600 group-hover:text-stone-300 ${quiet ? 'opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100' : ''}`}" />
</button>
