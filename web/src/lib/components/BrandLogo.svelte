<script lang="ts">
	import { API_BASE } from '$lib/config';
	import { platformInfo } from '$lib/stores/platform';

	export let className = 'h-10 w-auto';

	$: src = $platformInfo?.logo_url ? `${API_BASE}${$platformInfo.logo_url}` : '/logo.png';
	$: alt = $platformInfo?.name || 'Anvil';
</script>

<img {src} {alt} class="object-contain {className}" />
