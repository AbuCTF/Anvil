<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import type { VpnStatusResponse } from '$api';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { instantTitle } from '$lib/time';

	let vpnConfig: string | null = null;
	let vpnStatus: VpnStatusResponse | null = null;
	let loading = true;
	let generating = false;
	let regenerating = false;
	let error = '';
	let loadFailed = false;
	let statusError = '';
	let copied = false;
	let statusInterval: ReturnType<typeof setInterval>;
	let showRegenerateConfirm = false;

	onMount(async () => {
		if (await loadVPNData()) startStatusPolling();
	});

	function startStatusPolling() {
		if (statusInterval) clearInterval(statusInterval);
		statusInterval = setInterval(async () => {
			try {
				vpnStatus = await api.getVPNStatus();
				statusError = '';
			} catch (e) {
				statusError = e instanceof Error ? e.message : 'Unable to refresh VPN status';
			}
		}, 3000);
	}

	onDestroy(() => {
		if (statusInterval) clearInterval(statusInterval);
	});

	async function loadVPNData(): Promise<boolean> {
		try {
			const [configRes, statusRes] = await Promise.all([
				api.getVPNConfig(),
				api.getVPNStatus()
			]);

			vpnConfig = configRes.config_file ?? null;
			vpnStatus = statusRes || null;
			error = '';
			statusError = '';
			loadFailed = false;
			return true;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load VPN data';
			loadFailed = true;
			return false;
		} finally {
			loading = false;
		}
	}

	async function retryLoad() {
		loading = true;
		if (await loadVPNData()) startStatusPolling();
	}

	async function generateConfig() {
		generating = true;
		error = '';

		try {
			const response = await api.generateVPNConfig();
			if (response.config_file) {
				vpnConfig = response.config_file;
			}
			await loadVPNData();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to generate VPN config';
		} finally {
			generating = false;
		}
	}

	async function regenerateConfig() {
		regenerating = true;
		error = '';

		try {
			const response = await api.regenerateVPNConfig();
			if (response.config_file) {
				vpnConfig = response.config_file;
			}
			showRegenerateConfirm = false;
			await loadVPNData();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to regenerate VPN config';
		} finally {
			regenerating = false;
		}
	}

	function downloadConfig() {
		if (!vpnConfig) return;

		const blob = new Blob([vpnConfig], { type: 'text/plain' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = 'anvil.conf';
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
		URL.revokeObjectURL(url);
	}

	async function copyConfig() {
		if (!vpnConfig) return;
		try {
			await navigator.clipboard.writeText(vpnConfig);
			copied = true;
			setTimeout(() => copied = false, 2000);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to copy VPN config';
		}
	}

	function formatBytes(bytes: number): string {
		if (bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(k));
		return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
	}

	function formatLastHandshake(timestamp: number): string {
		if (!Number.isFinite(timestamp) || timestamp <= 0) return 'Never';
		const seconds = Math.max(0, Math.floor(Date.now() / 1000) - timestamp);
		if (seconds < 60) return `${seconds}s ago`;
		if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
		if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
		return `${Math.floor(seconds / 86400)}d ago`;
	}

	const btnBase =
		'flex items-center justify-center gap-2 px-4 py-2.5 rounded-md text-sm leading-none font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
	const btnPrimary = 'text-amber-500 border border-amber-500/40 hover:bg-amber-500/10';
	const btnNeutral = 'text-stone-300 border border-stone-800 hover:bg-stone-800/40 hover:text-stone-100';
	const btnDanger = 'text-down border border-down/30 hover:bg-down/10';
</script>

<svelte:head>
	<title>VPN - Anvil</title>
</svelte:head>

<div class="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<PageHeader title="VPN Connection" subtitle="Connect to the CTF lab network using WireGuard" />

	{#if loading}
		<div class="flex items-center justify-center py-16">
			<Icon icon="mdi:loading" class="w-6 h-6 text-stone-500 animate-spin" />
		</div>
	{:else if loadFailed}
		<Card title="VPN Unavailable">
			<div class="flex items-start gap-3">
				<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 text-down shrink-0 mt-0.5" />
				<div>
					<p class="text-down text-sm">{error}</p>
					<button on:click={retryLoad} class="mt-3 {btnBase} {btnNeutral} py-2">
						<Icon icon="mdi:refresh" class="w-3.5 h-3.5 shrink-0" />
						<span>Try again</span>
					</button>
				</div>
			</div>
		</Card>
	{:else}
		<div class="grid grid-cols-1 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] gap-4 sm:gap-6 items-start">
			<!-- Status -->
			<Card title="Connection Status" bodyClass="p-5 sm:p-6">
				{#if statusError}
					<div class="mb-4 border border-warn/30 bg-warn/5 rounded-md px-3 py-2 text-warn text-xs" aria-live="polite">
						{statusError}
					</div>
				{/if}
				{#if vpnStatus?.connected}
					<div class="flex items-center gap-2.5 mb-4 leading-none">
						<span class="w-2 h-2 rounded-full bg-up animate-pulse"></span>
						<span class="optical-label text-up font-medium">Connected</span>
					</div>

					<div class="space-y-3 bg-stone-950/50 border border-stone-800 rounded-md p-4">
						<div class="flex items-center justify-between gap-3">
							<span class="text-stone-500 leading-none flex items-center gap-2">
								<OpticalIcon icon="mdi:ip-network" size={12} box={14} />
								<span class="optical-label metadata-label">Internal IP</span>
							</span>
							<code class="font-mono text-sm text-stone-200 tabular-nums break-all text-right">{vpnStatus.ip_address}</code>
						</div>
						<div class="flex items-center justify-between gap-3">
							<span class="text-stone-500 leading-none flex items-center gap-2">
								<OpticalIcon icon="mdi:clock-outline" size={12} box={14} />
								<span class="optical-label metadata-label">Last handshake</span>
							</span>
							<span
								class="text-stone-300 text-sm font-mono tabular-nums"
								title={instantTitle(vpnStatus.last_handshake, 'seconds')}
							>{formatLastHandshake(vpnStatus.last_handshake ?? 0)}</span>
						</div>
						<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
							<span class="text-stone-500 leading-none flex items-center gap-2">
								<OpticalIcon icon="mdi:swap-vertical" size={12} box={14} />
								<span class="optical-label metadata-label">Data transfer</span>
							</span>
							<span class="self-end sm:self-auto text-stone-300 text-sm leading-none font-mono tabular-nums inline-flex items-center gap-1">
								<OpticalIcon icon="mdi:arrow-up" size={11.5} box={12} className="text-up" />
								<span class="optical-label whitespace-nowrap">{formatBytes(vpnStatus.bytes_sent || 0)}</span>
								<span class="optical-label text-stone-600">/</span>
								<OpticalIcon icon="mdi:arrow-down" size={11.5} box={12} className="text-info" />
								<span class="optical-label whitespace-nowrap">{formatBytes(vpnStatus.bytes_received || 0)}</span>
							</span>
						</div>
					</div>

					<p class="leading-none text-stone-600 mt-3 flex items-center gap-1.5">
						<OpticalIcon icon="mdi:information-outline" size={11} box={12} />
						<span class="optical-label metadata-label">Status updates every 3 seconds</span>
					</p>
				{:else}
					<div class="flex items-center gap-2.5 mb-4 leading-none">
						<span class="w-2 h-2 rounded-full bg-stone-600"></span>
						<span class="optical-label text-stone-400 font-medium">Not connected</span>
					</div>

					<div class="bg-stone-950/50 border border-stone-800 rounded-md p-4">
						<p class="text-stone-400 text-sm">
							{#if vpnConfig}
								Download and install the WireGuard configuration, then activate the tunnel to connect.
							{:else}
								Generate a VPN configuration first, then download and install it.
							{/if}
						</p>
					</div>

					{#if vpnStatus?.ip_address}
						<p class="text-stone-500 mt-3 flex items-baseline gap-2">
							<span class="optical-label metadata-label">Your assigned IP</span>
							<code class="text-stone-300 text-xs font-mono tabular-nums">{vpnStatus.ip_address}</code>
						</p>
					{/if}
				{/if}
			</Card>

			<!-- Configuration -->
			<Card title="Configuration" bodyClass="p-5 sm:p-6">
				{#if vpnConfig}
					<div class="space-y-4">
						<div class="flex gap-2">
							<button on:click={downloadConfig} class="flex-1 {btnBase} {btnPrimary}">
								<Icon icon="mdi:download" class="w-3.5 h-3.5 shrink-0" />
								<span>Download</span>
							</button>
							<button
								on:click={copyConfig}
								class="{btnBase} {btnNeutral} px-3"
								title="Copy to clipboard"
							>
								<Icon icon={copied ? 'mdi:check' : 'mdi:content-copy'} class="w-4 h-4" />
							</button>
						</div>

						<div class="relative">
							<pre class="bg-stone-950/60 border border-stone-800 rounded-md p-3 pt-9 text-xs text-stone-300 font-mono whitespace-pre-wrap break-all overflow-y-auto max-h-64">{vpnConfig}</pre>
							<span class="metadata-label absolute top-2.5 right-2.5 px-2 py-1 bg-stone-900 border border-stone-800 rounded text-stone-500">WireGuard</span>
						</div>

						{#if showRegenerateConfirm}
							<div class="border border-down/30 bg-down/5 rounded-md p-4">
								<p class="text-down text-sm mb-3 flex items-start gap-2">
									<Icon icon="mdi:alert-outline" class="w-4 h-4 shrink-0 mt-0.5" />
									<span>This will invalidate your current config. You'll need to update your WireGuard client.</span>
								</p>
								<div class="flex gap-2">
									<button on:click={regenerateConfig} disabled={regenerating} class="flex-1 {btnBase} {btnDanger} py-2">
										{#if regenerating}
										<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
											<span>Regenerating...</span>
										{:else}
										<Icon icon="mdi:refresh" class="w-3.5 h-3.5 shrink-0" />
											<span>Confirm regenerate</span>
										{/if}
									</button>
									<button on:click={() => showRegenerateConfirm = false} class="{btnBase} {btnNeutral} py-2">
										Cancel
									</button>
								</div>
							</div>
						{:else}
							<button on:click={() => showRegenerateConfirm = true} class="w-full {btnBase} {btnNeutral} py-2">
								<Icon icon="mdi:refresh" class="w-3.5 h-3.5 shrink-0" />
								<span>Regenerate config</span>
							</button>
						{/if}
					</div>
				{:else}
					<div class="space-y-4">
						<div class="bg-stone-950/50 border border-stone-800 rounded-md p-4">
							<p class="text-stone-400 text-sm mb-2">
								Generate a personal WireGuard configuration file to access the CTF network.
							</p>
							<ul class="text-xs text-stone-500 space-y-1 list-disc list-inside">
								<li>Unique to your account</li>
								<li>Required for accessing challenge instances</li>
								<li>Can be regenerated if needed</li>
							</ul>
						</div>
						<button on:click={generateConfig} disabled={generating} class="w-full {btnBase} {btnPrimary}">
							{#if generating}
								<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
								<span>Generating...</span>
							{:else}
								<Icon icon="mdi:key-plus" class="w-3.5 h-3.5 shrink-0" />
								<span>Generate config</span>
							{/if}
						</button>
					</div>
				{/if}
			</Card>
		</div>

		{#if error}
			<div class="mt-4 sm:mt-6 border border-down/30 bg-down/5 rounded-lg p-4 flex items-start gap-3">
				<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 text-down shrink-0 mt-0.5" />
				<span class="text-down text-sm">{error}</span>
			</div>
		{/if}
	{/if}
</div>
