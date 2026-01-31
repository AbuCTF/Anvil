<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let vpnConfig: string | null = null;
	let vpnStatus: any = null;
	let loading = true;
	let generating = false;
	let error = '';
	let copied = false;

	onMount(async () => {
		if (!$auth.isAuthenticated) {
			goto('/login');
			return;
		}
		await loadVPNData();
	});

	async function loadVPNData() {
		try {
			const [configRes, statusRes] = await Promise.all([
				api.getVPNConfig().catch(() => null),
				api.getVPNStatus().catch(() => null)
			]);

			vpnConfig = configRes?.config || null;
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
			const response = await api.generateVPNConfig();
			vpnConfig = response.config;
			await loadVPNData();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to generate VPN config';
		} finally {
			generating = false;
		}
	}

	function downloadConfig() {
		if (!vpnConfig) return;
		
		const blob = new Blob([vpnConfig], { type: 'text/plain' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = 'anvil.conf';
		a.click();
		URL.revokeObjectURL(url);
	}

	async function copyConfig() {
		if (!vpnConfig) return;
		await navigator.clipboard.writeText(vpnConfig);
		copied = true;
		setTimeout(() => copied = false, 2000);
	}
</script>

<svelte:head>
	<title>VPN - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	<div class="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<div class="mb-8">
			<h1 class="text-3xl font-bold text-white">VPN Connection</h1>
			<p class="text-stone-400 mt-2">Connect to the CTF lab network using WireGuard</p>
		</div>

		{#if loading}
			<div class="flex items-center justify-center min-h-[40vh]">
				<div class="text-center">
					<Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin mx-auto mb-4" />
					<p class="text-stone-500">Loading VPN data...</p>
				</div>
			</div>
		{:else}
			<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
				<!-- Status Card -->
				<div class="bg-stone-950 border border-stone-800 rounded-lg p-6">
					<h2 class="text-xl font-bold text-white mb-6 flex items-center space-x-2">
						<Icon icon="mdi:connection" class="w-6 h-6" />
						<span>Connection Status</span>
					</h2>

					{#if vpnStatus?.connected}
						<div class="flex items-center space-x-3 mb-6">
							<div class="w-4 h-4 bg-green-500 rounded-full animate-pulse"></div>
							<span class="text-green-400 font-semibold text-lg">Connected</span>
						</div>

						<div class="space-y-4 bg-black border border-stone-800 rounded-lg p-4">
							<div class="flex items-center justify-between">
								<span class="text-stone-500 flex items-center space-x-2">
									<Icon icon="mdi:ip-network" class="w-4 h-4" />
									<span>Internal IP</span>
								</span>
								<code class="text-green-400 font-mono">{vpnStatus.internal_ip}</code>
							</div>
							<div class="flex items-center justify-between">
								<span class="text-stone-500 flex items-center space-x-2">
									<Icon icon="mdi:clock-outline" class="w-4 h-4" />
									<span>Connected Since</span>
								</span>
								<span class="text-white">{vpnStatus.connected_since || 'N/A'}</span>
							</div>
							<div class="flex items-center justify-between">
								<span class="text-stone-500 flex items-center space-x-2">
									<Icon icon="mdi:swap-vertical" class="w-4 h-4" />
									<span>Data Transfer</span>
								</span>
								<span class="text-white">
									<Icon icon="mdi:arrow-up" class="w-3 h-3 inline text-green-400" /> {vpnStatus.bytes_sent || '0 B'} / 
									<Icon icon="mdi:arrow-down" class="w-3 h-3 inline text-blue-400" /> {vpnStatus.bytes_received || '0 B'}
								</span>
							</div>
						</div>
					{:else}
						<div class="flex items-center space-x-3 mb-6">
							<div class="w-4 h-4 bg-stone-600 rounded-full"></div>
							<span class="text-stone-500 font-semibold text-lg">Not Connected</span>
						</div>

						<div class="bg-stone-900/50 border border-stone-800 rounded-lg p-4">
							<p class="text-stone-400 text-sm">
								Download and install the WireGuard configuration to connect to the lab network.
							</p>
						</div>
					{/if}
				</div>

				<!-- Configuration Card -->
				<div class="bg-stone-950 border border-stone-800 rounded-lg p-6">
					<h2 class="text-xl font-bold text-white mb-6 flex items-center space-x-2">
						<Icon icon="mdi:file-cog" class="w-6 h-6" />
						<span>Configuration</span>
					</h2>

					{#if vpnConfig}
						<div class="space-y-4">
							<div class="flex space-x-3">
								<button
									on:click={downloadConfig}
									class="flex-1 flex items-center justify-center space-x-2 px-4 py-3 bg-white text-black rounded-lg font-medium hover:bg-stone-200 transition"
								>
									<Icon icon="mdi:download" class="w-5 h-5" />
									<span>Download</span>
								</button>
								<button
									on:click={copyConfig}
									class="px-4 py-3 bg-stone-900 text-white rounded-lg hover:bg-stone-800 transition border border-stone-800"
									title="Copy to clipboard"
								>
									<Icon icon={copied ? "mdi:check" : "mdi:content-copy"} class="w-5 h-5" />
								</button>
							</div>

							<div class="relative">
								<pre class="bg-black border border-stone-800 rounded-lg p-4 text-xs text-stone-300 overflow-x-auto max-h-64">{vpnConfig}</pre>
								<div class="absolute top-3 right-3">
									<span class="px-2 py-1 bg-stone-900 border border-stone-800 rounded text-xs text-stone-500">WireGuard</span>
								</div>
							</div>
						</div>
					{:else}
						<div class="space-y-4">
							<div class="bg-stone-900/50 border border-stone-800 rounded-lg p-4 mb-4">
								<p class="text-stone-400 text-sm mb-2">
									Generate a personal WireGuard configuration file to access the CTF network.
								</p>
								<ul class="text-xs text-stone-500 space-y-1 list-disc list-inside">
									<li>Unique to your account</li>
									<li>Required for accessing challenge instances</li>
									<li>Can be regenerated if needed</li>
								</ul>
							</div>
							<button
								on:click={generateConfig}
								disabled={generating}
								class="w-full flex items-center justify-center space-x-2 px-4 py-3 bg-white text-black rounded-lg font-medium hover:bg-stone-200 disabled:opacity-50 disabled:cursor-not-allowed transition"
							>
								{#if generating}
									<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
									<span>Generating...</span>
								{:else}
									<Icon icon="mdi:key-plus" class="w-5 h-5" />
									<span>Generate Config</span>
								{/if}
							</button>
						</div>
					{/if}
				</div>
			</div>

			<!-- Setup Instructions -->
			<div class="bg-stone-950 border border-stone-800 rounded-lg p-6">
				<h2 class="text-xl font-bold text-white mb-6 flex items-center space-x-2">
					<Icon icon="mdi:book-open-variant" class="w-6 h-6" />
					<span>Setup Instructions</span>
				</h2>

				<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
					<div class="space-y-4">
						<div class="flex items-center space-x-2 text-white font-semibold">
							<Icon icon="mdi:linux" class="w-6 h-6" />
							<h3>Linux</h3>
						</div>
						<div class="bg-black border border-stone-800 rounded-lg p-4 space-y-3 text-sm">
							<div>
								<p class="text-stone-500 text-xs mb-1">Install WireGuard</p>
								<code class="block text-green-400">sudo apt install wireguard</code>
							</div>
							<div>
								<p class="text-stone-500 text-xs mb-1">Copy config</p>
								<code class="block text-green-400">sudo cp anvil.conf /etc/wireguard/</code>
							</div>
							<div>
								<p class="text-stone-500 text-xs mb-1">Connect</p>
								<code class="block text-green-400">sudo wg-quick up anvil</code>
							</div>
						</div>
					</div>

					<div class="space-y-4">
						<div class="flex items-center space-x-2 text-white font-semibold">
							<Icon icon="mdi:apple" class="w-6 h-6" />
							<h3>macOS</h3>
						</div>
						<div class="bg-black border border-stone-800 rounded-lg p-4 space-y-3 text-sm">
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">1.</span>
								<p class="text-stone-300">Install WireGuard from the App Store</p>
							</div>
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">2.</span>
								<p class="text-stone-300">Open app and click "Import tunnel(s) from file"</p>
							</div>
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">3.</span>
								<p class="text-stone-300">Select downloaded config and activate</p>
							</div>
						</div>
					</div>

					<div class="space-y-4">
						<div class="flex items-center space-x-2 text-white font-semibold">
							<Icon icon="mdi:microsoft-windows" class="w-6 h-6" />
							<h3>Windows</h3>
						</div>
						<div class="bg-black border border-stone-800 rounded-lg p-4 space-y-3 text-sm">
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">1.</span>
								<p class="text-stone-300">Download WireGuard for Windows</p>
							</div>
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">2.</span>
								<p class="text-stone-300">Click "Add Tunnel" → "Import from file"</p>
							</div>
							<div class="flex items-start space-x-2">
								<span class="text-stone-500 flex-shrink-0">3.</span>
								<p class="text-stone-300">Select config and activate the tunnel</p>
							</div>
						</div>
					</div>
				</div>
			</div>

			{#if error}
				<div class="mt-6 bg-red-950/30 border border-red-900 rounded-lg p-4 flex items-center space-x-3">
					<Icon icon="mdi:alert-circle" class="w-5 h-5 text-red-400 flex-shrink-0" />
					<span class="text-red-400">{error}</span>
				</div>
			{/if}
		{/if}
	</div>
</div>
