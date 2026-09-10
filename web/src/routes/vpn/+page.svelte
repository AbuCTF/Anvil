<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';

	// The VPN endpoints return a superset of the base client types; describe the
	// fields this page reads so property access is type-checked.
	interface VpnConfigResponse {
		config?: string;
		config_file?: string;
		assigned_ip?: string;
		ip_address?: string;
		has_config?: boolean;
	}
	interface VpnStatus {
		connected?: boolean;
		ip_address?: string;
		last_handshake?: number;
		bytes_sent?: number;
		bytes_received?: number;
	}

	let vpnConfig: string | null = null;
	let vpnStatus: VpnStatus | null = null;
	let loading = true;
	let generating = false;
	let regenerating = false;
	let error = '';
	let copied = false;
	let statusInterval: ReturnType<typeof setInterval>;
	let showRegenerateConfirm = false;

	onMount(async () => {
		await loadVPNData();

		// Poll status frequently for responsive connect/disconnect state.
		statusInterval = setInterval(async () => {
			try {
				vpnStatus = await api.getVPNStatus();
			} catch (e) {
				// Silently fail status checks
			}
		}, 3000);
	});

	onDestroy(() => {
		if (statusInterval) clearInterval(statusInterval);
	});

	async function loadVPNData() {
		try {
			const [configRes, statusRes] = await Promise.all([
				api.getVPNConfig().catch(() => null),
				api.getVPNStatus().catch(() => null)
			]);

			const cfg = configRes as VpnConfigResponse | null;
			if (cfg?.config_file) {
				vpnConfig = cfg.config_file;
			} else if (cfg?.has_config === false) {
				vpnConfig = null;
			}
			vpnStatus = statusRes || null;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load VPN data';
		} finally {
			loading = false;
		}
	}

	async function generateConfig() {
		generating = true;
		error = '';

		try {
			const response = (await api.generateVPNConfig()) as VpnConfigResponse;
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
			const response = (await api.regenerateVPNConfig()) as VpnConfigResponse;
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
		await navigator.clipboard.writeText(vpnConfig);
		copied = true;
		setTimeout(() => copied = false, 2000);
	}

	function formatBytes(bytes: number): string {
		if (bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(k));
		return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
	}

	function formatLastHandshake(timestamp: number): string {
		if (!timestamp) return 'Never';
		const seconds = Math.floor(Date.now() / 1000) - timestamp;
		if (seconds < 60) return `${seconds}s ago`;
		if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
		return `${Math.floor(seconds / 3600)}h ago`;
	}

	const btnBase =
		'flex items-center justify-center gap-2 px-4 py-2.5 rounded-md text-sm font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
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
	{:else}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6 mb-4 sm:mb-6">
			<!-- Status -->
			<Card title="Connection Status">
				{#if vpnStatus?.connected}
					<div class="flex items-center gap-2.5 mb-4">
						<span class="w-2 h-2 rounded-full bg-up animate-pulse"></span>
						<span class="text-up font-medium">Connected</span>
					</div>

					<div class="space-y-3 bg-stone-950/50 border border-stone-800 rounded-md p-4">
						<div class="flex items-center justify-between gap-3">
							<span class="text-stone-500 text-sm flex items-center gap-2">
								<Icon icon="mdi:ip-network" class="w-4 h-4" />
								<span>Internal IP</span>
							</span>
							<code class="font-mono text-sm text-stone-200 tabular-nums break-all text-right">{vpnStatus.ip_address}</code>
						</div>
						<div class="flex items-center justify-between gap-3">
							<span class="text-stone-500 text-sm flex items-center gap-2">
								<Icon icon="mdi:clock-outline" class="w-4 h-4" />
								<span>Last handshake</span>
							</span>
							<span class="text-stone-300 text-sm font-mono tabular-nums">{formatLastHandshake(vpnStatus.last_handshake ?? 0)}</span>
						</div>
						<div class="flex items-center justify-between gap-3">
							<span class="text-stone-500 text-sm flex items-center gap-2">
								<Icon icon="mdi:swap-vertical" class="w-4 h-4" />
								<span>Data transfer</span>
							</span>
							<span class="text-stone-300 text-sm font-mono tabular-nums">
								<Icon icon="mdi:arrow-up" class="w-3 h-3 inline text-up" />
								{formatBytes(vpnStatus.bytes_sent || 0)}
								<span class="text-stone-600">/</span>
								<Icon icon="mdi:arrow-down" class="w-3 h-3 inline text-info" />
								{formatBytes(vpnStatus.bytes_received || 0)}
							</span>
						</div>
					</div>

					<p class="text-xs text-stone-600 mt-3 flex items-center gap-1.5">
						<Icon icon="mdi:information-outline" class="w-3.5 h-3.5" />
						<span>Status updates every 10 seconds</span>
					</p>
				{:else}
					<div class="flex items-center gap-2.5 mb-4">
						<span class="w-2 h-2 rounded-full bg-stone-600"></span>
						<span class="text-stone-400 font-medium">Not connected</span>
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
						<p class="text-xs text-stone-500 mt-3">
							Your assigned IP: <code class="text-stone-300 font-mono tabular-nums">{vpnStatus.ip_address}</code>
						</p>
					{/if}
				{/if}
			</Card>

			<!-- Configuration -->
			<Card title="Configuration">
				{#if vpnConfig}
					<div class="space-y-4">
						<div class="flex gap-2">
							<button on:click={downloadConfig} class="flex-1 {btnBase} {btnPrimary}">
								<Icon icon="mdi:download" class="w-4 h-4" />
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
							<span class="absolute top-2.5 right-2.5 px-2 py-0.5 bg-stone-900 border border-stone-800 rounded text-[0.65rem] uppercase tracking-wide text-stone-500">WireGuard</span>
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
											<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
											<span>Regenerating...</span>
										{:else}
											<Icon icon="mdi:refresh" class="w-4 h-4" />
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
								<Icon icon="mdi:refresh" class="w-4 h-4" />
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
								<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
								<span>Generating...</span>
							{:else}
								<Icon icon="mdi:key-plus" class="w-4 h-4" />
								<span>Generate config</span>
							{/if}
						</button>
					</div>
				{/if}
			</Card>
		</div>

		<!-- Setup Instructions -->
		<Card title="Setup Instructions">
			<div class="grid grid-cols-1 md:grid-cols-3 gap-4 sm:gap-6">
				<div class="space-y-3">
					<h3 class="text-stone-200 text-sm font-medium uppercase tracking-wide">GNU/Linux</h3>
					<div class="bg-stone-950/50 border border-stone-800 rounded-md p-4 space-y-3 text-sm">
						<div>
							<p class="text-stone-500 text-xs mb-1">Install WireGuard</p>
							<code class="block text-stone-300 font-mono text-xs break-all">sudo apt install wireguard</code>
						</div>
						<div>
							<p class="text-stone-500 text-xs mb-1">Copy config</p>
							<code class="block text-stone-300 font-mono text-xs break-all">sudo cp anvil.conf /etc/wireguard/</code>
						</div>
						<div>
							<p class="text-stone-500 text-xs mb-1">Connect</p>
							<code class="block text-stone-300 font-mono text-xs break-all">sudo wg-quick up anvil</code>
						</div>
					</div>
				</div>

				<div class="space-y-3">
					<h3 class="text-stone-200 text-sm font-medium uppercase tracking-wide">macOS</h3>
					<div class="bg-stone-950/50 border border-stone-800 rounded-md p-4 space-y-3 text-sm">
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">1.</span>
							<p class="text-stone-300">Install WireGuard from the App Store</p>
						</div>
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">2.</span>
							<p class="text-stone-300">Open app and click "Import tunnel(s) from file"</p>
						</div>
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">3.</span>
							<p class="text-stone-300">Select downloaded config and activate</p>
						</div>
					</div>
				</div>

				<div class="space-y-3">
					<h3 class="text-stone-200 text-sm font-medium uppercase tracking-wide">Windows</h3>
					<div class="bg-stone-950/50 border border-stone-800 rounded-md p-4 space-y-3 text-sm">
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">1.</span>
							<p class="text-stone-300">Download WireGuard for Windows</p>
						</div>
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">2.</span>
							<p class="text-stone-300">Click "Add Tunnel", then choose "Import from file"</p>
						</div>
						<div class="flex items-start gap-2">
							<span class="text-stone-600 font-mono tabular-nums shrink-0">3.</span>
							<p class="text-stone-300">Select config and activate the tunnel</p>
						</div>
					</div>
				</div>
			</div>
		</Card>

		{#if error}
			<div class="mt-4 sm:mt-6 border border-down/30 bg-down/5 rounded-lg p-4 flex items-center gap-3">
				<Icon icon="mdi:alert-circle-outline" class="w-5 h-5 text-down shrink-0" />
				<span class="text-down text-sm">{error}</span>
			</div>
		{/if}
	{/if}
</div>
