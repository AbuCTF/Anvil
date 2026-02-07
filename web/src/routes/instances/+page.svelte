<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	interface Instance {
		id: string;
		challenge_name: string;
		challenge_slug: string;
		status: string;
		ip_address: string;
		ports: Record<string, number>;
		created_at: string;
		expires_at: number;
		extensions_used: number;
		max_extensions: number;
	}

	let instances: Instance[] = [];
	let loading = true;
	let error = '';
	let actionLoading: Record<string, string> = {};

	let refreshInterval: ReturnType<typeof setInterval>;

	onMount(async () => {
			return;
		}
		await loadInstances();
		refreshInterval = setInterval(loadInstances, 30000);
	});

	onDestroy(() => {
		if (refreshInterval) {
			clearInterval(refreshInterval);
		}
	});

	async function loadInstances() {
		try {
			const response = await api.getInstances();
			instances = response.instances || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load instances';
		} finally {
			loading = false;
		}
	}

	async function extendInstance(instanceId: string) {
		actionLoading[instanceId] = 'extending';
		try {
			const result = await api.extendInstance(instanceId);
			const instance = instances.find(i => i.id === instanceId);
			if (instance) {
				instance.expires_at = result.new_expires_at;
				instance.extensions_used++;
				instances = [...instances];
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to extend instance';
		} finally {
			delete actionLoading[instanceId];
			actionLoading = { ...actionLoading };
		}
	}

	async function stopInstance(instanceId: string) {
		actionLoading[instanceId] = 'stopping';
		try {
			await api.stopInstance(instanceId);
			instances = instances.filter(i => i.id !== instanceId);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to stop instance';
		} finally {
			delete actionLoading[instanceId];
			actionLoading = { ...actionLoading };
		}
	}

	function formatTimeRemaining(expiresAt: number): string {
		const now = Math.floor(Date.now() / 1000);
		const remaining = expiresAt - now;
		
		if (remaining <= 0) return 'Expired';
		
		const hours = Math.floor(remaining / 3600);
		const minutes = Math.floor((remaining % 3600) / 60);
		
		if (hours > 0) {
			return `${hours}h ${minutes}m`;
		}
		return `${minutes}m`;
	}

	function getStatusColor(status: string): string {
		switch (status) {
			case 'running':
				return 'bg-green-500';
			case 'starting':
			case 'stopping':
				return 'bg-yellow-500 animate-pulse';
			case 'stopped':
			case 'error':
				return 'bg-red-500';
			default:
				return 'bg-stone-500';
		}
	}

	function getStatusIcon(status: string): string {
		switch (status) {
			case 'running':
				return 'mdi:check-circle';
			case 'starting':
				return 'mdi:loading';
			case 'stopping':
				return 'mdi:stop-circle';
			case 'stopped':
			case 'error':
				return 'mdi:alert-circle';
			default:
				return 'mdi:help-circle';
		}
	}
</script>

<svelte:head>
	<title>My Instances - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<div class="mb-8 flex items-center justify-between">
			<div>
				<h1 class="text-3xl font-bold text-white">My Instances</h1>
				<p class="text-stone-400 mt-2">Manage your running challenge instances</p>
			</div>
			<a 
				href="/challenges"
				class="flex items-center space-x-2 px-6 py-3 bg-white text-black rounded-lg font-medium hover:bg-stone-200 transition"
			>
				<Icon icon="mdi:plus" class="w-5 h-5" />
				<span>New Instance</span>
			</a>
		</div>

		{#if loading}
			<div class="flex items-center justify-center min-h-[40vh]">
				<div class="text-center">
					<Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin mx-auto mb-4" />
					<p class="text-stone-500">Loading instances...</p>
				</div>
			</div>
		{:else if error}
			<div class="bg-red-950/30 border border-red-900 rounded-lg p-6 text-center">
				<Icon icon="mdi:alert-circle" class="w-8 h-8 text-red-400 mx-auto mb-3" />
				<p class="text-red-400 mb-4">{error}</p>
				<button
					on:click={loadInstances}
					class="px-6 py-3 bg-stone-950 text-white rounded-lg hover:bg-stone-900 transition border border-stone-800"
				>
					Try Again
				</button>
			</div>
		{:else if instances.length === 0}
			<div class="bg-stone-950 border border-stone-800 rounded-lg p-12 text-center">
				<Icon icon="mdi:server-off" class="w-16 h-16 text-stone-700 mx-auto mb-4" />
				<h2 class="text-xl font-semibold text-white mb-2">No Active Instances</h2>
				<p class="text-stone-500 mb-6">
					Start an instance from a challenge to begin
				</p>
				<a 
					href="/challenges"
					class="inline-flex items-center space-x-2 px-6 py-3 bg-white text-black rounded-lg font-medium hover:bg-stone-200 transition"
				>
					<Icon icon="mdi:flag" class="w-5 h-5" />
					<span>Browse Challenges</span>
				</a>
			</div>
		{:else}
			<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
				{#each instances as instance (instance.id)}
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="p-5 border-b border-stone-800 flex items-center justify-between">
							<div class="flex items-center space-x-3">
								<div class="w-2.5 h-2.5 rounded-full {getStatusColor(instance.status)}"></div>
								<a 
									href="/challenges/{instance.challenge_slug}"
									class="text-white font-semibold hover:text-stone-200 transition"
								>
									{instance.challenge_name}
								</a>
							</div>
							<div class="flex items-center space-x-2 text-xs text-stone-400">
								<Icon icon={getStatusIcon(instance.status)} class="w-4 h-4" />
								<span class="capitalize">{instance.status}</span>
							</div>
						</div>

						<div class="p-5 space-y-4">
							<div class="flex items-center justify-between">
								<span class="text-stone-500 text-sm">IP Address</span>
								<div class="flex items-center space-x-2">
									<code class="px-3 py-1.5 bg-black text-green-400 border border-stone-800 rounded font-mono text-sm">
										{instance.ip_address}
									</code>
									<button 
										on:click={() => navigator.clipboard.writeText(instance.ip_address)}
										class="p-1.5 bg-black border border-stone-800 rounded text-stone-400 hover:text-white transition"
										title="Copy IP"
									>
										<Icon icon="mdi:content-copy" class="w-4 h-4" />
									</button>
								</div>
							</div>

							{#if instance.ports && Object.keys(instance.ports).length > 0}
								<div>
									<span class="text-stone-500 text-sm block mb-3">Exposed Ports</span>
									<div class="flex flex-wrap gap-2">
										{#each Object.entries(instance.ports) as [service, port]}
											<span class="inline-flex items-center px-3 py-1.5 bg-black text-stone-300 border border-stone-800 rounded text-sm">
												<Icon icon="mdi:ethernet" class="w-4 h-4 mr-1.5 text-stone-500" />
												<span class="text-stone-500">{service}:</span>
												<code class="text-white ml-1">{port}</code>
											</span>
										{/each}
									</div>
								</div>
							{/if}

							<div class="grid grid-cols-2 gap-4 pt-2">
								<div class="p-3 bg-black border border-stone-800 rounded-lg">
									<div class="flex items-center space-x-2 text-stone-500 text-xs mb-1">
										<Icon icon="mdi:clock-outline" class="w-4 h-4" />
										<span>Time Remaining</span>
									</div>
									<div class="text-white font-semibold text-lg">
										{formatTimeRemaining(instance.expires_at)}
									</div>
								</div>

								<div class="p-3 bg-black border border-stone-800 rounded-lg">
									<div class="flex items-center space-x-2 text-stone-500 text-xs mb-1">
										<Icon icon="mdi:refresh" class="w-4 h-4" />
										<span>Extensions</span>
									</div>
									<div class="text-white font-semibold text-lg">
										{instance.extensions_used} / {instance.max_extensions}
									</div>
								</div>
							</div>
						</div>

						<div class="p-4 border-t border-stone-800 flex space-x-3">
							<button
								on:click={() => extendInstance(instance.id)}
								disabled={!!actionLoading[instance.id] || instance.extensions_used >= instance.max_extensions}
								class="flex-1 flex items-center justify-center space-x-2 px-4 py-3 bg-stone-900 text-white rounded-lg hover:bg-stone-800 disabled:opacity-50 disabled:cursor-not-allowed transition border border-stone-800"
							>
								{#if actionLoading[instance.id] === 'extending'}
									<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
									<span>Extending...</span>
								{:else}
									<Icon icon="mdi:clock-plus" class="w-5 h-5" />
									<span>Extend</span>
								{/if}
							</button>
							<button
								on:click={() => stopInstance(instance.id)}
								disabled={!!actionLoading[instance.id]}
								class="flex-1 flex items-center justify-center space-x-2 px-4 py-3 bg-red-950/30 text-red-400 rounded-lg hover:bg-red-950/50 disabled:opacity-50 transition border border-red-900"
							>
								{#if actionLoading[instance.id] === 'stopping'}
									<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
									<span>Stopping...</span>
								{:else}
									<Icon icon="mdi:stop" class="w-5 h-5" />
									<span>Stop</span>
								{/if}
							</button>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
