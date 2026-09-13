<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { instantTitle } from '$lib/time';

	interface Instance {
		id: string;
		challenge_name: string;
		challenge_slug: string;
		status: string;
		ip_address: string;
		ports: Record<string, number>;
		created_at: number | string;
		expires_at: number;
		extensions_used: number;
		max_extensions: number;
		reset_count: number;
		max_resets: number;
	}

	let instances: Instance[] = [];
	let loading = true;
	let error = '';
	let actionError = '';
	let copiedKey = '';
	let actionLoading: Record<string, string> = {};

	let refreshInterval: ReturnType<typeof setInterval>;
	let instancesRequest: AbortController | null = null;
	let instancesRequestID = 0;
	let disposed = false;
	const REQUEST_TIMEOUT_MS = 10000;

	onMount(() => {
		disposed = false;
		document.addEventListener('visibilitychange', refreshVisibleInstances);
		loadInstances().then(() => {
			if (!disposed) refreshInterval = setInterval(refreshVisibleInstances, 30000);
		});
		return () => {
			disposed = true;
			instancesRequest?.abort();
			if (refreshInterval) clearInterval(refreshInterval);
			document.removeEventListener('visibilitychange', refreshVisibleInstances);
		};
	});

	function refreshVisibleInstances() {
		if (document.visibilityState === 'visible') loadInstances();
	}

	async function loadInstances(force = false) {
		if (instancesRequest) {
			if (!force) return;
			instancesRequest.abort();
		}
		const controller = new AbortController();
		const requestID = ++instancesRequestID;
		instancesRequest = controller;
		let timedOut = false;
		const requestTimeout = setTimeout(() => {
			timedOut = true;
			controller.abort();
		}, REQUEST_TIMEOUT_MS);
		try {
			const response = await api.getInstances({ signal: controller.signal });
			if (disposed || requestID !== instancesRequestID) return;
			instances = response.instances || [];
			error = '';
		} catch (e) {
			if (disposed || requestID !== instancesRequestID || (controller.signal.aborted && !timedOut)) return;
			const message = timedOut
				? 'Instance refresh timed out'
				: e instanceof Error
					? e.message
					: 'Failed to load instances';
			if (instances.length === 0) error = message;
			else actionError = `Unable to refresh instances: ${message}`;
		} finally {
			clearTimeout(requestTimeout);
			if (instancesRequest === controller) instancesRequest = null;
			if (!disposed && requestID === instancesRequestID) loading = false;
		}
	}

	async function extendInstance(instanceId: string) {
		actionLoading[instanceId] = 'extending';
		actionError = '';
		try {
			const result = await api.extendInstance(instanceId);
			const instance = instances.find(i => i.id === instanceId);
			if (instance) {
				instance.expires_at = result.new_expires_at;
				instance.extensions_used++;
				instances = [...instances];
			}
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Failed to extend instance';
		} finally {
			delete actionLoading[instanceId];
			actionLoading = { ...actionLoading };
		}
	}

	async function stopInstance(instanceId: string) {
		actionLoading[instanceId] = 'stopping';
		actionError = '';
		try {
			await api.stopInstance(instanceId);
			instances = instances.filter(i => i.id !== instanceId);
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Failed to stop instance';
		} finally {
			delete actionLoading[instanceId];
			actionLoading = { ...actionLoading };
		}
	}

	async function revertInstance(instanceId: string) {
		actionLoading[instanceId] = 'reverting';
		actionError = '';
		try {
			await api.revertInstance(instanceId);
			await loadInstances(true);
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Failed to revert instance';
			await loadInstances(true);
		} finally {
			delete actionLoading[instanceId];
			actionLoading = { ...actionLoading };
		}
	}

	async function copyText(value: string) {
		try {
			await navigator.clipboard.writeText(value);
			copiedKey = value;
			setTimeout(() => {
				if (copiedKey === value) copiedKey = '';
			}, 2000);
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Failed to copy to clipboard';
		}
	}

	function getConnectionCmd(ip: string, portKey: string): string {
		const [port, svc] = portKey.split('/');
		if (svc === 'http' || svc === 'https') {
			return `${svc}://${ip}:${port}`;
		}
		return `nc ${ip} ${port}`;
	}

	function isHttpPort(portKey: string): boolean {
		const [, svc] = portKey.split('/');
		return svc === 'http' || svc === 'https';
	}

	function formatTimeRemaining(expiresAt: number): string {
		if (!Number.isFinite(expiresAt) || expiresAt <= 0) return '—';
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

	// Muted status dot — the semantic color lives on the dot, the label stays gray.
	function statusDot(status: string): string {
		switch (status) {
			case 'running':
				return 'bg-up';
			case 'starting':
			case 'stopping':
				return 'bg-warn animate-pulse';
			case 'stopped':
			case 'error':
				return 'bg-down';
			default:
				return 'bg-stone-600';
		}
	}

	const btnBase =
		'flex items-center justify-center gap-1.5 px-3 py-2.5 rounded-md text-sm leading-none font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
	const btnNeutral = 'text-stone-300 border border-stone-800 hover:bg-stone-800/40 hover:text-stone-100';
	const btnDanger = 'text-down border border-down/30 hover:bg-down/10';
</script>

<svelte:head>
	<title>My Instances - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
	<PageHeader title="My Instances" subtitle={instances.length ? `${instances.length} active` : ''}>
		<a
			slot="actions"
			href="/challenges"
			class="inline-flex items-center gap-2 px-4 py-2 rounded-md border border-stone-800 text-stone-200 hover:bg-stone-800/40 hover:text-stone-100 text-sm leading-none font-medium transition-colors"
		>
			<Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />
			<span>New Instance</span>
		</a>
	</PageHeader>

	{#if actionError && !loading}
		<div class="mb-4 border border-down/30 bg-down/5 rounded-lg px-4 py-3 flex items-start justify-between gap-3" aria-live="polite">
			<div class="flex items-start gap-2 min-w-0">
				<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 text-down shrink-0 mt-0.5" />
				<p class="text-down text-sm">{actionError}</p>
			</div>
			<button on:click={() => actionError = ''} class="text-stone-500 hover:text-stone-200" aria-label="Dismiss error">
				<Icon icon="mdi:close" class="w-4 h-4" />
			</button>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-16">
			<Icon icon="mdi:loading" class="w-6 h-6 text-stone-500 animate-spin" />
		</div>
	{:else if error}
		<div class="border border-down/30 bg-down/5 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle-outline" class="w-8 h-8 text-down mx-auto mb-3" />
			<p class="text-down text-sm mb-4">{error}</p>
			<button
				on:click={() => { error = ''; loading = true; loadInstances(true); }}
				class="inline-flex items-center gap-2 px-4 py-2 rounded-md border border-stone-800 text-stone-200 hover:bg-stone-800/40 hover:text-stone-100 text-sm leading-none font-medium transition-colors"
			>
				<Icon icon="mdi:refresh" class="w-3.5 h-3.5 shrink-0" />
				<span>Try again</span>
			</button>
		</div>
	{:else if instances.length === 0}
		<EmptyState icon="mdi:server-off" text="No active instances.">
			<a
				href="/challenges"
				class="mt-4 inline-flex items-center gap-2 px-4 py-2 rounded-md border border-stone-800 text-stone-200 hover:bg-stone-800/40 hover:text-stone-100 text-sm leading-none font-medium transition-colors"
			>
				<Icon icon="mdi:magnify" class="w-3.5 h-3.5 shrink-0" />
				<span>Browse challenges</span>
			</a>
		</EmptyState>
	{:else}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6">
			{#each instances as instance (instance.id)}
				{@const expired = instance.expires_at <= Math.floor(Date.now() / 1000)}
				{@const busy = !!actionLoading[instance.id]}
				{@const resetCount = instance.reset_count ?? 0}
				{@const maxResets = instance.max_resets ?? 3}
				{@const resetLimitReached = resetCount >= maxResets}
				<Card bodyClass="p-0">
					<div slot="header" class="flex items-center gap-2.5 min-w-0 leading-none">
						<span class="w-2 h-2 rounded-full shrink-0 {statusDot(instance.status)}"></span>
						<a
							href="/challenges/{instance.challenge_slug}"
							class="optical-label text-stone-200 font-medium truncate hover:text-amber-400 transition-colors"
						>
							{instance.challenge_name}
						</a>
					</div>
					<span slot="meta" class="text-xs text-stone-400 capitalize tracking-wide">{instance.status}</span>

					<div class="p-4 sm:p-5 space-y-4">
						<!-- Target -->
						<div class="flex items-center justify-between gap-3">
							<span class="metadata-label text-stone-500 shrink-0">Target</span>
							<div class="flex items-center gap-1.5 min-w-0">
								<code class="min-w-0 break-all px-2.5 py-1.5 bg-stone-950/60 text-stone-300 border border-stone-800 rounded-md font-mono text-sm tabular-nums">
									{instance.ip_address}
								</code>
								<button
									on:click={() => copyText(instance.ip_address)}
									class="p-1.5 text-stone-600 hover:text-stone-300 transition-colors shrink-0"
									title="Copy"
									aria-label="Copy target address"
								>
									<Icon icon={copiedKey === instance.ip_address ? 'mdi:check' : 'mdi:content-copy'} class="w-3.5 h-3.5" />
								</button>
							</div>
						</div>

						<!-- Connect -->
						{#if instance.ports && Object.keys(instance.ports).length > 0}
							<div>
								<span class="metadata-label text-stone-500 block mb-2">Connect</span>
								<div class="space-y-1.5">
									{#each Object.entries(instance.ports) as [portKey]}
										<div class="flex items-center gap-1.5">
											{#if isHttpPort(portKey)}
												<a
													href={getConnectionCmd(instance.ip_address, portKey)}
													target="_blank"
													rel="noopener"
													class="flex-1 min-w-0 break-all px-2.5 py-1.5 bg-stone-950/60 text-stone-300 border border-stone-800 rounded-md font-mono text-sm hover:text-amber-400 transition-colors"
												>
													{getConnectionCmd(instance.ip_address, portKey)}
												</a>
											{:else}
												<code class="flex-1 min-w-0 break-all px-2.5 py-1.5 bg-stone-950/60 text-stone-300 border border-stone-800 rounded-md font-mono text-sm">
													{getConnectionCmd(instance.ip_address, portKey)}
												</code>
											{/if}
											<button
												on:click={() => copyText(getConnectionCmd(instance.ip_address, portKey))}
												class="p-1.5 text-stone-600 hover:text-stone-300 transition-colors shrink-0"
												title="Copy"
												aria-label="Copy connection command"
											>
												<Icon icon={copiedKey === getConnectionCmd(instance.ip_address, portKey) ? 'mdi:check' : 'mdi:content-copy'} class="w-3.5 h-3.5" />
											</button>
										</div>
									{/each}
								</div>
							</div>
						{/if}

						<!-- Stats -->
						<div class="grid grid-cols-2 gap-3 pt-1">
							<div class="p-3 bg-stone-950/50 border border-stone-800 rounded-md">
								<div class="flex items-center gap-1.5 text-stone-500 mb-1.5">
									<OpticalIcon icon="mdi:clock-outline" size={12} box={12} />
									<span class="optical-label metadata-label">Remaining</span>
								</div>
								<div
									class="font-mono tabular-nums text-lg {expired ? 'text-down' : 'text-amber-500/90'}"
									title={instantTitle(instance.expires_at, 'seconds')}
								>
									{formatTimeRemaining(instance.expires_at)}
								</div>
							</div>

							<div class="p-3 bg-stone-950/50 border border-stone-800 rounded-md">
								<div class="flex items-center gap-1.5 text-stone-500 mb-1.5">
									<OpticalIcon icon="mdi:refresh" size={12} box={12} />
									<span class="optical-label metadata-label">Extensions</span>
								</div>
								<div class="font-mono tabular-nums text-lg text-stone-100">
									{instance.extensions_used} / {instance.max_extensions}
								</div>
							</div>
						</div>
					</div>

					<!-- Actions -->
					<div class="px-4 py-3 sm:px-5 border-t border-stone-800 grid grid-cols-3 gap-2">
						<button
							on:click={() => extendInstance(instance.id)}
							disabled={busy || instance.extensions_used >= instance.max_extensions}
							class="{btnBase} {btnNeutral}"
						>
							{#if actionLoading[instance.id] === 'extending'}
								<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
							{:else}
								<Icon icon="mdi:clock-plus" class="w-3.5 h-3.5 shrink-0" />
							{/if}
							<span>Extend</span>
						</button>
						<button
							on:click={() => revertInstance(instance.id)}
							disabled={busy || resetLimitReached}
							title={resetLimitReached ? `Reset limit reached (${resetCount}/${maxResets})` : 'Revert instance'}
							aria-label={resetLimitReached ? `Reset limit reached (${resetCount}/${maxResets})` : 'Revert instance'}
							class="{btnBase} {btnNeutral}"
						>
							{#if actionLoading[instance.id] === 'reverting'}
								<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
							{:else}
								<Icon icon="mdi:restart" class="w-3.5 h-3.5 shrink-0" />
							{/if}
							<span>Revert</span>
						</button>
						<button
							on:click={() => stopInstance(instance.id)}
							disabled={busy}
							class="{btnBase} {btnDanger}"
						>
							{#if actionLoading[instance.id] === 'stopping'}
								<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
							{:else}
								<Icon icon="mdi:stop" class="w-3.5 h-3.5 shrink-0" />
							{/if}
							<span>Stop</span>
						</button>
					</div>
				</Card>
			{/each}
		</div>
	{/if}
</div>
