<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let challenge: any = null;
	let instance: any = null;
	let loading = true;
	let error = '';
	let cooldownInfo: { until: number; remaining: number } | null = null;

	// Flag submission
	let flagInput = '';
	let submitting = false;
	let submitResult: { correct: boolean; message: string } | null = null;

	// Instance management
	let creatingInstance = false;
	let instanceAction = '';
	let timerInterval: ReturnType<typeof setInterval>;
	let timeRemaining = '';

	// Admin editing
	let isEditing = false;
	let editForm: any = null;
	let saving = false;
	let showEditSuccess = false;

	// Flag editing
	let editingFlags: any[] = [];
	let showFlagModal = false;
	let newFlag = { name: '', flag: '', points: 100 };
	let savingFlag = false;

	const slug = $page.params.slug;
	
	$: if (!slug) {
		error = 'Invalid challenge';
	}

	$: isAdmin = $auth.isAuthenticated && $auth.user?.role === 'admin';

	const difficultyConfig: Record<string, { color: string; bg: string; border: string }> = {
		easy: { color: 'text-green-400', bg: 'bg-green-500/10', border: 'border-green-500/20' },
		medium: { color: 'text-yellow-400', bg: 'bg-yellow-500/10', border: 'border-yellow-500/20' },
		hard: { color: 'text-orange-400', bg: 'bg-orange-500/10', border: 'border-orange-500/20' },
		insane: { color: 'text-red-400', bg: 'bg-red-500/10', border: 'border-red-500/20' }
	};

	onMount(async () => {
		await loadChallenge();
		if ($auth.isAuthenticated) {
			await loadUserInstance();
		}
		
		// Update timer every second
		timerInterval = setInterval(() => {
			if (instance?.expires_at) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
				// Auto-reload if expired
				if (instance.expires_at < Math.floor(Date.now() / 1000)) {
					instance = null;
					loadUserInstance();
				}
			}
			// Update cooldown
			if (cooldownInfo && cooldownInfo.until > Math.floor(Date.now() / 1000)) {
				cooldownInfo.remaining = cooldownInfo.until - Math.floor(Date.now() / 1000);
			} else if (cooldownInfo) {
				cooldownInfo = null;
			}
		}, 1000);
	});

	onDestroy(() => {
		if (timerInterval) clearInterval(timerInterval);
	});

	async function loadChallenge() {
		if (!slug) return;
		try {
			challenge = await api.getChallenge(slug as string);
			editForm = {
				name: challenge.name,
				description: challenge.description || '',
				difficulty: challenge.difficulty,
				base_points: challenge.base_points
			};
			// Load editable flags for admin
			if (challenge.flags) {
				editingFlags = challenge.flags.map((f: any) => ({ ...f, editing: false, newFlag: '' }));
			}
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
			if (instance) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
			}
		} catch (e) {
			console.error('Failed to load instances', e);
		}
	}

	async function submitFlag() {
		if (!slug || !flagInput.trim()) return;
		submitting = true;
		submitResult = null;

		try {
			const result = await api.submitFlag(slug as string, flagInput.trim());
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
		if (!slug) return;
		creatingInstance = true;
		error = '';
		try {
			const result = await api.createInstance(slug as string);
			instance = result.instance;
			if (instance) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
			}
		} catch (e: any) {
			// Check if it's a cooldown error
			if (e.cooldown_until) {
				cooldownInfo = {
					until: e.cooldown_until,
					remaining: e.remaining_seconds
				};
			}
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
			instance = { ...instance, expires_at: result.new_expires_at, extensions_used: result.extensions_used };
			timeRemaining = formatTimeRemaining(result.new_expires_at);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to extend';
		} finally {
			instanceAction = '';
		}
	}

	async function stopInstance() {
		if (!instance || !confirm('Stop this instance? You will have a cooldown period before starting again.')) return;
		instanceAction = 'stopping';
		try {
			const result = await api.stopInstance(instance.id);
			instance = null;
			// Set cooldown info from response
			if (result.cooldown_until) {
				cooldownInfo = {
					until: result.cooldown_until,
					remaining: result.cooldown_minutes * 60
				};
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to stop';
		} finally {
			instanceAction = '';
		}
	}

	async function handleSaveEdit() {
		if (!challenge || !editForm) return;
		saving = true;
		try {
			await api.updateAdminChallenge(challenge.id, {
				name: editForm.name,
				description: editForm.description,
				difficulty: editForm.difficulty,
				base_points: parseInt(editForm.base_points)
			});
			await loadChallenge();
			isEditing = false;
			showEditSuccess = true;
			setTimeout(() => showEditSuccess = false, 3000);
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to save');
		} finally {
			saving = false;
		}
	}

	async function handlePublish() {
		if (!challenge) return;
		saving = true;
		try {
			await api.publishChallenge(challenge.id);
			await loadChallenge();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to publish');
		} finally {
			saving = false;
		}
	}

	async function handleUnpublish() {
		if (!challenge) return;
		saving = true;
		try {
			await api.unpublishChallenge(challenge.id);
			await loadChallenge();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to unpublish');
		} finally {
			saving = false;
		}
	}

	function cancelEdit() {
		isEditing = false;
		editForm = {
			name: challenge.name,
			description: challenge.description || '',
			difficulty: challenge.difficulty,
			base_points: challenge.base_points
		};
	}

	function formatTimeRemaining(expiresAt: number): string {
		const now = Math.floor(Date.now() / 1000);
		const remaining = expiresAt - now;
		if (remaining <= 0) return 'Expired';
		const hours = Math.floor(remaining / 3600);
		const minutes = Math.floor((remaining % 3600) / 60);
		const seconds = remaining % 60;
		if (hours > 0) {
			return `${hours}h ${minutes}m ${seconds}s`;
		}
		return `${minutes}m ${seconds}s`;
	}

	function formatCooldown(seconds: number): string {
		if (seconds <= 0) return '0:00';
		const mins = Math.floor(seconds / 60);
		const secs = seconds % 60;
		return `${mins}:${secs.toString().padStart(2, '0')}`;
	}

	function copyToClipboard(text: string) {
		navigator.clipboard.writeText(text);
	}

	// Flag management functions
	async function saveFlag(flag: any) {
		if (!challenge) return;
		savingFlag = true;
		try {
			await api.updateFlag(challenge.id, flag.id, {
				name: flag.name,
				flag: flag.newFlag || flag.flag,
				points: flag.points
			});
			await loadChallenge();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to update flag');
		} finally {
			savingFlag = false;
		}
	}

	async function createNewFlag() {
		if (!challenge) return;
		savingFlag = true;
		try {
			await api.createFlag(challenge.id, newFlag);
			newFlag = { name: '', flag: '', points: 100 };
			showFlagModal = false;
			await loadChallenge();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to create flag');
		} finally {
			savingFlag = false;
		}
	}

	async function deleteFlag(flagId: string) {
		if (!challenge || !confirm('Delete this flag?')) return;
		savingFlag = true;
		try {
			await api.deleteFlag(challenge.id, flagId);
			await loadChallenge();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to delete flag');
		} finally {
			savingFlag = false;
		}
	}
</script>

<svelte:head>
	<title>{challenge?.name || 'Challenge'} - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	{#if loading}
		<div class="flex items-center justify-center min-h-[60vh]">
			<Icon icon="mdi:loading" class="w-8 h-8 text-stone-600 animate-spin" />
		</div>
	{:else if error && !challenge}
		<div class="max-w-2xl mx-auto px-4 py-20 text-center">
			<Icon icon="mdi:alert-circle-outline" class="w-12 h-12 text-red-500/50 mx-auto mb-4" />
			<h2 class="text-lg font-medium text-white mb-2">Challenge Not Found</h2>
			<p class="text-stone-500 text-sm mb-6">{error}</p>
			<a href="/challenges" class="text-stone-400 hover:text-white text-sm transition">
				← Back to challenges
			</a>
		</div>
	{:else if challenge}
		<!-- Success Toast -->
		{#if showEditSuccess}
			<div class="fixed top-4 right-4 z-50 bg-green-500/10 border border-green-500/20 text-green-400 px-4 py-2 rounded-lg text-sm flex items-center gap-2">
				<Icon icon="mdi:check" class="w-4 h-4" />
				Saved
			</div>
		{/if}

		<div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
			<!-- Back Link -->
			<a href="/challenges" class="inline-flex items-center gap-1.5 text-stone-500 hover:text-stone-300 text-sm mb-8 transition">
				<Icon icon="mdi:chevron-left" class="w-4 h-4" />
				Challenges
			</a>

			<div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
				<!-- Main Content -->
				<div class="lg:col-span-2 space-y-6">
					<!-- Header -->
					<div class="flex items-start justify-between gap-4">
						<div class="flex-1">
							{#if isEditing}
								<input
									type="text"
									bind:value={editForm.name}
									class="text-2xl font-semibold bg-transparent border-b border-stone-700 text-white w-full focus:outline-none focus:border-stone-500 pb-1"
								/>
							{:else}
								<h1 class="text-2xl font-semibold text-white">{challenge.name}</h1>
							{/if}
							
							<div class="flex items-center gap-3 mt-3">
								{#if isEditing}
									<select bind:value={editForm.difficulty} class="text-xs px-2 py-1 rounded bg-stone-900 border border-stone-700 text-stone-300 focus:outline-none">
										<option value="easy">Easy</option>
										<option value="medium">Medium</option>
										<option value="hard">Hard</option>
										<option value="insane">Insane</option>
									</select>
								{:else}
									<span class="text-xs font-medium px-2 py-0.5 rounded {difficultyConfig[challenge.difficulty]?.bg} {difficultyConfig[challenge.difficulty]?.border} {difficultyConfig[challenge.difficulty]?.color} border">
										{challenge.difficulty}
									</span>
								{/if}
								
								<span class="text-xs text-stone-500">
									{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}
								</span>
								
								{#if challenge.category}
									<span class="text-xs text-stone-500">• {challenge.category}</span>
								{/if}

								{#if challenge.status === 'draft'}
									<span class="text-xs font-medium px-2 py-0.5 rounded bg-yellow-500/10 border border-yellow-500/20 text-yellow-400">
										Draft
									</span>
								{/if}

								{#if challenge.is_solved}
									<span class="text-xs font-medium px-2 py-0.5 rounded bg-green-500/10 border border-green-500/20 text-green-400 flex items-center gap-1">
										<Icon icon="mdi:check" class="w-3 h-3" />
										Solved
									</span>
								{/if}
							</div>
						</div>

						<div class="text-right">
							{#if isEditing}
								<input
									type="number"
									bind:value={editForm.base_points}
									class="w-16 text-xl font-bold bg-transparent border-b border-stone-700 text-white text-right focus:outline-none focus:border-stone-500"
								/>
								<p class="text-xs text-stone-500 mt-1">points</p>
							{:else}
								<p class="text-xl font-bold text-white">{challenge.base_points}</p>
								<p class="text-xs text-stone-500">points</p>
							{/if}
						</div>
					</div>

					<!-- Admin Controls -->
					{#if isAdmin}
						<div class="flex items-center gap-2 py-3 border-y border-stone-800/50">
							{#if isEditing}
								<button on:click={handleSaveEdit} disabled={saving} class="text-xs px-3 py-1.5 bg-white text-black rounded font-medium hover:bg-stone-200 disabled:opacity-50 transition flex items-center gap-1.5">
									{#if saving}<Icon icon="mdi:loading" class="w-3 h-3 animate-spin" />{/if}
									Save
								</button>
								<button on:click={cancelEdit} class="text-xs px-3 py-1.5 text-stone-400 hover:text-white transition">
									Cancel
								</button>
							{:else}
								<button on:click={() => isEditing = true} class="text-xs px-3 py-1.5 text-stone-400 hover:text-white transition flex items-center gap-1.5">
									<Icon icon="mdi:pencil" class="w-3 h-3" />
									Edit
								</button>
								{#if challenge.status === 'draft'}
									<button on:click={handlePublish} disabled={saving} class="text-xs px-3 py-1.5 text-green-400 hover:text-green-300 transition flex items-center gap-1.5 disabled:opacity-50">
										<Icon icon="mdi:eye" class="w-3 h-3" />
										Publish
									</button>
								{:else}
									<button on:click={handleUnpublish} disabled={saving} class="text-xs px-3 py-1.5 text-yellow-400 hover:text-yellow-300 transition flex items-center gap-1.5 disabled:opacity-50">
										<Icon icon="mdi:eye-off" class="w-3 h-3" />
										Unpublish
									</button>
								{/if}
								<a href="/admin" class="text-xs px-3 py-1.5 text-stone-500 hover:text-stone-300 transition ml-auto">
									Admin Panel →
								</a>
							{/if}
						</div>
					{/if}

					<!-- Description -->
					<div>
						<h2 class="text-xs font-medium text-stone-500 uppercase tracking-wider mb-3">Description</h2>
						{#if isEditing}
							<textarea
								bind:value={editForm.description}
								rows="6"
								class="w-full px-4 py-3 bg-stone-950 border border-stone-800 rounded-lg text-stone-300 text-sm leading-relaxed focus:outline-none focus:border-stone-700 resize-none"
								placeholder="Challenge description..."
							></textarea>
						{:else if challenge.description}
							<p class="text-stone-400 text-sm leading-relaxed whitespace-pre-wrap">{challenge.description}</p>
						{:else}
							<p class="text-stone-600 text-sm italic">No description provided.</p>
						{/if}
					</div>

					<!-- Objectives -->
					{#if (challenge.flags && challenge.flags.length > 0) || (isEditing && isAdmin)}
						<div>
							<div class="flex items-center justify-between mb-3">
								<h2 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Objectives</h2>
								{#if isEditing && isAdmin}
									<button 
										on:click={() => showFlagModal = true}
										class="text-xs text-emerald-400 hover:text-emerald-300 transition flex items-center gap-1"
									>
										<Icon icon="mdi:plus" class="w-3.5 h-3.5" />
										Add Flag
									</button>
								{:else}
									<span class="text-xs text-stone-600">{challenge.user_solves || 0}/{challenge.total_flags}</span>
								{/if}
							</div>
							
							<div class="space-y-2">
								{#if isEditing && isAdmin}
									<!-- Admin flag editing mode -->
									{#each editingFlags as flag, i}
										<div class="py-3 px-4 bg-stone-950 border border-stone-800 rounded-lg">
											{#if flag.editing}
												<div class="space-y-3">
													<div class="grid grid-cols-2 gap-3">
														<input
															type="text"
															bind:value={flag.name}
															placeholder="Flag name"
															class="px-3 py-2 bg-stone-900 border border-stone-700 rounded text-sm text-stone-200 focus:outline-none focus:border-stone-600"
														/>
														<input
															type="number"
															bind:value={flag.points}
															placeholder="Points"
															class="px-3 py-2 bg-stone-900 border border-stone-700 rounded text-sm text-stone-200 focus:outline-none focus:border-stone-600"
														/>
													</div>
													<input
														type="text"
														bind:value={flag.newFlag}
														placeholder="New flag value (leave empty to keep current)"
														class="w-full px-3 py-2 bg-stone-900 border border-stone-700 rounded text-sm text-stone-200 focus:outline-none focus:border-stone-600 font-mono"
													/>
													<div class="flex items-center gap-2 pt-1">
														<button
															on:click={() => { saveFlag(flag); flag.editing = false; }}
															disabled={savingFlag}
															class="px-3 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white text-xs rounded transition disabled:opacity-50"
														>
															{savingFlag ? 'Saving...' : 'Save'}
														</button>
														<button
															on:click={() => { flag.editing = false; }}
															class="px-3 py-1.5 bg-stone-700 hover:bg-stone-600 text-stone-300 text-xs rounded transition"
														>
															Cancel
														</button>
														<button
															on:click={() => deleteFlag(flag.id)}
															disabled={savingFlag}
															class="ml-auto px-3 py-1.5 text-red-400 hover:text-red-300 text-xs transition disabled:opacity-50"
														>
															Delete
														</button>
													</div>
												</div>
											{:else}
												<div class="flex items-center justify-between">
													<div class="flex items-center gap-3">
														<div class="w-6 h-6 rounded flex items-center justify-center bg-stone-800 text-stone-500 text-xs font-medium">
															{i + 1}
														</div>
														<span class="text-sm text-stone-300">{flag.name}</span>
													</div>
													<div class="flex items-center gap-3">
														<span class="text-xs text-stone-500">{flag.points} pts</span>
														<button
															on:click={() => { flag.editing = true; flag.newFlag = ''; }}
															class="text-xs text-stone-400 hover:text-stone-200 transition"
														>
															<Icon icon="mdi:pencil" class="w-4 h-4" />
														</button>
													</div>
												</div>
											{/if}
										</div>
									{/each}
									{#if editingFlags.length === 0}
										<p class="text-stone-600 text-sm py-4 text-center">No flags. Click "Add Flag" to create one.</p>
									{/if}
								{:else}
									<!-- Normal user view -->
									{#each challenge.flags as flag, i}
										<div class="flex items-center justify-between py-3 px-4 rounded-lg {flag.is_solved ? 'bg-green-500/5 border border-green-500/10' : 'bg-stone-950 border border-stone-800'}">
											<div class="flex items-center gap-3">
												<div class="w-6 h-6 rounded flex items-center justify-center {flag.is_solved ? 'bg-green-500/20 text-green-400' : 'bg-stone-800 text-stone-500'} text-xs font-medium">
													{#if flag.is_solved}
														<Icon icon="mdi:check" class="w-3.5 h-3.5" />
													{:else}
														{i + 1}
													{/if}
												</div>
												<span class="text-sm {flag.is_solved ? 'text-green-400' : 'text-stone-300'}">{flag.name}</span>
											</div>
											<span class="text-xs {flag.is_solved ? 'text-green-400/60' : 'text-stone-500'}">{flag.points} pts</span>
										</div>
									{/each}
								{/if}
							</div>
						</div>
					{/if}

					<!-- Hints -->
					{#if challenge.hints && challenge.hints.length > 0}
						<div>
							<h2 class="text-xs font-medium text-stone-500 uppercase tracking-wider mb-3">Hints</h2>
							<div class="space-y-2">
								{#each challenge.hints as hint, i}
									<div class="py-3 px-4 bg-stone-950 border border-stone-800 rounded-lg">
										{#if hint.is_unlocked}
											<p class="text-stone-400 text-sm">{hint.content}</p>
										{:else}
											<div class="flex items-center justify-between">
												<span class="text-stone-500 text-sm">Hint #{i + 1}</span>
												<button class="text-xs text-yellow-500 hover:text-yellow-400 transition">
													Unlock ({hint.cost} pts)
												</button>
											</div>
										{/if}
									</div>
								{/each}
							</div>
						</div>
					{/if}
				</div>

				<!-- Sidebar -->
				<div class="space-y-6">
					<!-- Instance Panel -->
					{#if $auth.isAuthenticated}
						<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
							<div class="px-4 py-3 border-b border-stone-800">
								<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Instance</h3>
							</div>
							<div class="p-4">
								{#if instance}
									<div class="space-y-4">
										<div class="flex items-center gap-2">
											<span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
											<span class="text-green-400 text-sm font-medium">Running</span>
										</div>
										
										<div>
											<p class="text-xs text-stone-500 mb-1">IP Address</p>
											<div class="flex items-center justify-between bg-black rounded px-3 py-2">
												<code class="text-sm text-white font-mono">{instance.ip_address}</code>
												<button on:click={() => copyToClipboard(instance.ip_address)} class="text-stone-500 hover:text-white transition">
													<Icon icon="mdi:content-copy" class="w-4 h-4" />
												</button>
											</div>
										</div>

										<div>
											<p class="text-xs text-stone-500 mb-1">Time Remaining</p>
											{@const seconds = instance.expires_at - Math.floor(Date.now() / 1000)}
											<p class="text-lg font-mono {seconds < 300 ? 'text-red-400 animate-pulse' : seconds < 600 ? 'text-yellow-400' : 'text-white'}">{timeRemaining}</p>
											{#if seconds < 300}
												<p class="text-xs text-red-400 mt-1">Instance will shut down soon!</p>
											{/if}
										</div>

										<div>
											<p class="text-xs text-stone-500 mb-1">Extensions</p>
											<p class="text-sm text-stone-400">{instance.extensions_used || 0} / {instance.max_extensions || 3} used</p>
										</div>

										<div class="flex gap-2">
											<button on:click={extendInstance} disabled={instanceAction === 'extending' || (instance.extensions_used >= (instance.max_extensions || 3))} class="flex-1 text-xs py-2 bg-stone-900 text-stone-300 rounded hover:bg-stone-800 transition disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1">
												{#if instanceAction === 'extending'}
													<Icon icon="mdi:loading" class="w-3 h-3 animate-spin" />
												{:else}
													<Icon icon="mdi:clock-plus-outline" class="w-3 h-3" />
												{/if}
												{instanceAction === 'extending' ? 'Extending...' : 'Extend'}
											</button>
											<button on:click={stopInstance} disabled={instanceAction === 'stopping'} class="flex-1 text-xs py-2 bg-red-500/10 text-red-400 rounded hover:bg-red-500/20 transition disabled:opacity-50 flex items-center justify-center gap-1">
												{#if instanceAction === 'stopping'}
													<Icon icon="mdi:loading" class="w-3 h-3 animate-spin" />
												{:else}
													<Icon icon="mdi:stop" class="w-3 h-3" />
												{/if}
												{instanceAction === 'stopping' ? 'Stopping...' : 'Stop'}
											</button>
										</div>
									</div>
								{:else if cooldownInfo}
									<!-- Cooldown State -->
									<div class="text-center py-4">
										<div class="w-12 h-12 rounded-full bg-yellow-500/10 flex items-center justify-center mx-auto mb-3">
											<Icon icon="mdi:timer-sand" class="w-6 h-6 text-yellow-500" />
										</div>
										<p class="text-yellow-400 text-sm font-medium mb-1">Cooldown Active</p>
										<p class="text-2xl font-mono text-yellow-400 mb-2">{formatCooldown(cooldownInfo.remaining)}</p>
										<p class="text-stone-500 text-xs">You can start a new instance after the cooldown period.</p>
									</div>
								{:else}
									<div class="text-center py-4">
										<p class="text-stone-500 text-sm mb-4">No active instance</p>
										{#if error}
											<div class="mb-4 py-2 px-3 rounded text-sm bg-red-500/10 text-red-400">{error}</div>
										{/if}
										<button on:click={startInstance} disabled={creatingInstance} class="w-full py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition disabled:opacity-50 flex items-center justify-center gap-2">
											{#if creatingInstance}
												<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
												Starting...
											{:else}
												<Icon icon="mdi:play" class="w-4 h-4" />
												Start Instance
											{/if}
										</button>
									</div>
								{/if}
							</div>
						</div>
					{/if}

					<!-- Submit Flag -->
					{#if $auth.isAuthenticated}
						<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
							<div class="px-4 py-3 border-b border-stone-800">
								<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Submit Flag</h3>
							</div>
							<div class="p-4">
								<form on:submit|preventDefault={submitFlag} class="space-y-3">
									<input
										type="text"
										bind:value={flagInput}
										placeholder="flag&#123;...&#125;"
										class="w-full px-3 py-2.5 bg-black border border-stone-800 rounded text-white text-sm font-mono placeholder-stone-600 focus:outline-none focus:border-stone-700"
									/>
									<button type="submit" disabled={submitting || !flagInput.trim()} class="w-full py-2.5 bg-stone-900 text-white text-sm font-medium rounded hover:bg-stone-800 transition disabled:opacity-50 disabled:cursor-not-allowed">
										{submitting ? 'Checking...' : 'Submit'}
									</button>
								</form>
								
								{#if submitResult}
									<div class="mt-3 py-2 px-3 rounded text-sm {submitResult.correct ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'}">
										{submitResult.message}
									</div>
								{/if}
							</div>
						</div>
					{:else}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-6 text-center">
							<p class="text-stone-500 text-sm mb-4">Login to start this challenge</p>
							<a href="/login" class="inline-block w-full py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition">
								Login
							</a>
						</div>
					{/if}

					<!-- Stats -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Statistics</h3>
						</div>
						<div class="p-4 space-y-3">
							<div class="flex justify-between text-sm">
								<span class="text-stone-500">Solves</span>
								<span class="text-stone-300">{challenge.total_solves}</span>
							</div>
							<div class="flex justify-between text-sm">
								<span class="text-stone-500">Flags</span>
								<span class="text-stone-300">{challenge.total_flags}</span>
							</div>
							<div class="flex justify-between text-sm">
								<span class="text-stone-500">Type</span>
								<span class="text-stone-300">{challenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker'}</span>
							</div>
							{#if challenge.author_name}
								<div class="flex justify-between text-sm">
									<span class="text-stone-500">Author</span>
									<span class="text-stone-300">{challenge.author_name}</span>
								</div>
							{/if}
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}
</div>

<!-- Add Flag Modal -->
{#if showFlagModal}
	<div class="fixed inset-0 bg-black/80 flex items-center justify-center z-50 p-4">
		<div class="bg-stone-900 border border-stone-800 rounded-lg w-full max-w-md">
			<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between">
				<h3 class="text-sm font-medium text-stone-200">Add New Flag</h3>
				<button on:click={() => showFlagModal = false} class="text-stone-500 hover:text-stone-300 transition">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>
			<form on:submit|preventDefault={createNewFlag} class="p-4 space-y-4">
				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Name</label>
					<input
						type="text"
						bind:value={newFlag.name}
						placeholder="e.g., User Flag"
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded text-sm text-stone-200 focus:outline-none focus:border-stone-600"
					/>
				</div>
				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Flag Value</label>
					<input
						type="text"
						bind:value={newFlag.flag}
						placeholder="flag&#123;...&#125;"
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded text-sm text-stone-200 font-mono focus:outline-none focus:border-stone-600"
					/>
				</div>
				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Points</label>
					<input
						type="number"
						bind:value={newFlag.points}
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded text-sm text-stone-200 focus:outline-none focus:border-stone-600"
					/>
				</div>
				<div class="flex items-center gap-3 pt-2">
					<button
						type="submit"
						disabled={savingFlag}
						class="flex-1 py-2 bg-emerald-600 hover:bg-emerald-500 text-white text-sm font-medium rounded transition disabled:opacity-50"
					>
						{savingFlag ? 'Creating...' : 'Create Flag'}
					</button>
					<button
						type="button"
						on:click={() => showFlagModal = false}
						class="flex-1 py-2 bg-stone-700 hover:bg-stone-600 text-stone-300 text-sm font-medium rounded transition"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}