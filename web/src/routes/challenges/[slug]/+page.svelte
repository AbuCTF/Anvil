<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let challenge: any = null;
	let instance: any = null;
	let loading = true;
	let error = '';

	// Flag submission
	let flagInput = '';
	let submitting = false;
	let submitResult: { correct: boolean; message: string } | null = null;

	// Instance management
	let creatingInstance = false;
	let instanceAction = '';

	const slug = $page.params.slug;

	onMount(async () => {
		await loadChallenge();
		if ($auth.isAuthenticated) {
			await loadUserInstance();
		}
	});

	async function loadChallenge() {
		try {
			challenge = await api.getChallenge(slug);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load challenge';
		} finally {
			loading = false;
		}
	}

	async function loadUserInstance() {
		try {
			const response = await api.getInstances();
			instance = response.instances?.find((i: any) => 
				i.challenge_slug === slug && i.status === 'running'
			);
		} catch (e) {
			console.error('Failed to load instances', e);
		}
	}

	async function submitFlag() {
		if (!flagInput.trim()) return;
		
		submitting = true;
		submitResult = null;

		try {
			const result = await api.submitFlag(slug, flagInput.trim());
			submitResult = result;
			if (result.correct) {
				flagInput = '';
				await loadChallenge();
			}
		} catch (e) {
			submitResult = {
				correct: false,
				message: e instanceof Error ? e.message : 'Submission failed'
			};
		} finally {
			submitting = false;
		}
	}

	async function startInstance() {
		creatingInstance = true;
		try {
			const result = await api.createInstance(slug);
			instance = result.instance;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to start instance';
		} finally {
			creatingInstance = false;
		}
	}

	async function extendInstance() {
		if (!instance) return;
		instanceAction = 'extending';
		try {
			const result = await api.extendInstance(instance.id);
			instance = { ...instance, expires_at: result.new_expires_at };
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to extend instance';
		} finally {
			instanceAction = '';
		}
	}

	async function stopInstance() {
		if (!instance) return;
		instanceAction = 'stopping';
		try {
			await api.stopInstance(instance.id);
			instance = null;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to stop instance';
		} finally {
			instanceAction = '';
		}
	}

	function formatTimeRemaining(expiresAt: number): string {
		const now = Math.floor(Date.now() / 1000);
		const remaining = expiresAt - now;
		if (remaining <= 0) return 'Expired';
		
		const hours = Math.floor(remaining / 3600);
		const minutes = Math.floor((remaining % 3600) / 60);
		return `${hours}h ${minutes}m`;
	}
</script>

<svelte:head>
	<title>{challenge?.name || 'Challenge'} - Anvil</title>
</svelte:head>

{#if loading}
	<div class="flex items-center justify-center min-h-[60vh] font-mono">
		<p class="text-stone-500">LOADING...</p>
	</div>
{:else if error && !challenge}
	<div class="max-w-4xl mx-auto px-4 py-8 font-mono">
		<div class="bg-stone-900 border border-stone-800 p-6 text-center">
			<p class="text-stone-300">{error}</p>
			<a href="/challenges" class="inline-block mt-4 text-stone-300 hover:text-stone-100">
				← BACK TO CHALLENGES
			</a>
		</div>
	</div>
{:else if challenge}
	<div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8 font-mono">
		<nav class="mb-6">
			<a href="/challenges" class="text-stone-500 hover:text-stone-300 flex items-center space-x-1">
				<span>← BACK</span>
			</a>
		</nav>

		<div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
			<div class="lg:col-span-2 space-y-6">
				<div class="bg-stone-900 border border-stone-800 p-6">
					<div class="flex items-start justify-between mb-4">
						<div>
							<h1 class="text-2xl font-bold text-stone-100 flex items-center space-x-3">
								<span>{challenge.name}</span>
								{#if challenge.is_solved}
									<span class="text-stone-500 text-base">✓</span>
								{/if}
							</h1>
							{#if challenge.author_name}
								<p class="text-stone-500 text-sm mt-1">by {challenge.author_name}</p>
							{/if}
						</div>

						<span class="inline-flex items-center px-2 py-1 text-xs font-medium border border-stone-800 text-stone-300 uppercase">
							{challenge.difficulty}
						</span>
					</div>

					{#if challenge.description}
						<p class="text-stone-300 leading-relaxed">
							{challenge.description}
						</p>
					{/if}

					<div class="flex flex-wrap items-center gap-2 mt-4">
						{#if challenge.category}
							<span class="inline-flex items-center px-2 py-0.5 text-xs font-medium bg-stone-900 text-stone-500 border border-stone-800">
								{challenge.category}
							</span>
						{/if}
						<span class="inline-flex items-center px-2 py-0.5 text-xs font-medium text-stone-300 border border-stone-800">
							{challenge.base_points} pts
						</span>
						<span class="inline-flex items-center px-2 py-0.5 text-xs font-medium bg-stone-900 text-stone-500 border border-stone-800">
							{challenge.total_solves} solves
						</span>
					</div>
				</div>

				{#if challenge.flags && challenge.flags.length > 0}
					<div class="bg-stone-900 border border-stone-800 p-6">
						<h2 class="text-lg font-semibold text-stone-100 mb-4">
							FLAGS ({challenge.user_solves || 0}/{challenge.total_flags})
						</h2>

						<div class="space-y-3">
							{#each challenge.flags as flag}
								<div class="flex items-center justify-between p-3 {flag.is_solved ? 'bg-stone-800 border border-stone-700' : 'bg-stone-950 border border-stone-800'}">
									<div class="flex items-center space-x-3">
										<span class="text-stone-500 text-sm">{flag.is_solved ? '✓' : '○'}</span>
										<span class="text-stone-300">{flag.name}</span>
									</div>
									<span class="text-stone-500 font-medium">{flag.points} pts</span>
								</div>
							{/each}
						</div>
					</div>
				{/if}

				{#if challenge.hints && challenge.hints.length > 0}
					<div class="bg-stone-900 border border-stone-800 p-6">
						<h2 class="text-lg font-semibold text-stone-100 mb-4">
							HINTS
						</h2>

						<div class="space-y-3">
							{#each challenge.hints as hint, i}
								<div class="p-3 bg-stone-950 border border-stone-800">
									{#if hint.is_unlocked}
										<p class="text-stone-300">{hint.content}</p>
									{:else}
										<div class="flex items-center justify-between">
											<span class="text-stone-600">HINT #{i + 1}</span>
											<button
												class="px-3 py-1 bg-stone-900 text-stone-300 text-sm hover:bg-stone-800 border border-stone-800"
											>
												UNLOCK ({hint.cost} pts)
											</button>
										</div>
									{/if}
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>

			<div class="space-y-6">
				{#if $auth.isAuthenticated}
					<div class="bg-stone-900 border border-stone-800 p-6">
						<h2 class="text-lg font-semibold text-stone-100 mb-4">
							INSTANCE
						</h2>

						{#if instance}
							<div class="space-y-4">
								<div class="flex items-center space-x-2">
									<span class="w-2 h-2 bg-stone-300 animate-pulse"></span>
									<span class="text-stone-300 text-sm">RUNNING</span>
								</div>

								<div class="bg-stone-950 border border-stone-800 p-4 space-y-2">
									<div class="flex items-center justify-between text-sm">
										<span class="text-stone-600">IP</span>
										<code class="text-stone-300 bg-stone-900 px-2 py-0.5">
											{instance.ip_address}
										</code>
									</div>
									<div class="flex items-center justify-between text-sm">
										<span class="text-stone-600">TIME</span>
										<span class="text-stone-300">{formatTimeRemaining(instance.expires_at)}</span>
									</div>
								</div>

								<div class="flex space-x-2">
									<button
										on:click={extendInstance}
										disabled={instanceAction === 'extending'}
										class="flex-1 px-4 py-2 bg-stone-900 text-stone-300 hover:bg-stone-800 disabled:opacity-50 border border-stone-800"
									>
										{instanceAction === 'extending' ? 'EXTENDING...' : 'EXTEND'}
									</button>
									<button
										on:click={stopInstance}
										disabled={instanceAction === 'stopping'}
										class="flex-1 px-4 py-2 bg-stone-900 text-stone-300 hover:bg-stone-800 disabled:opacity-50 border border-stone-800"
									>
										{instanceAction === 'stopping' ? 'STOPPING...' : 'STOP'}
									</button>
								</div>
							</div>
						{:else}
							<p class="text-stone-500 text-sm mb-4">
								Start an instance to begin
							</p>
							<button
								on:click={startInstance}
								disabled={creatingInstance}
								class="w-full px-4 py-3 bg-stone-100 text-stone-950 font-medium hover:bg-stone-200 disabled:opacity-50"
							>
								{creatingInstance ? 'STARTING...' : 'START INSTANCE'}
							</button>
						{/if}
					</div>
				{/if}

				{#if $auth.isAuthenticated}
					<div class="bg-stone-900 border border-stone-800 p-6">
						<h2 class="text-lg font-semibold text-stone-100 mb-4">
							SUBMIT FLAG
						</h2>

						<form on:submit|preventDefault={submitFlag} class="space-y-4">
							<input
								type="text"
								bind:value={flagInput}
								placeholder="flag..."
								class="w-full px-3 py-2 bg-stone-950 border border-stone-800 text-stone-100 placeholder-stone-600 focus:outline-none focus:border-stone-700"
							/>

							<button
								type="submit"
								disabled={submitting || !flagInput.trim()}
								class="w-full px-4 py-2 bg-stone-100 text-stone-950 font-medium hover:bg-stone-200 disabled:opacity-50"
							>
								{submitting ? 'CHECKING...' : 'SUBMIT'}
							</button>
						</form>

						{#if submitResult}
							<div class="mt-4 p-3 border {submitResult.correct ? 'bg-stone-800 border-stone-700 text-stone-300' : 'bg-stone-900 border-stone-800 text-stone-500'}">
								{submitResult.message}
							</div>
						{/if}
					</div>
				{:else}
					<div class="bg-stone-900 border border-stone-800 p-6 text-center">
						<p class="text-stone-500 mb-4">LOGIN TO START</p>
						<a href="/login" class="inline-block px-6 py-2 bg-stone-100 text-stone-950 font-medium hover:bg-stone-200">
							LOGIN
						</a>
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}
