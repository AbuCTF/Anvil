<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import Icon from '@iconify/svelte';

	let activeTab = 'overview';
	let loading = true;
	let stats: any = null;
	let users: any[] = [];
	let challenges: any[] = [];
	let error = '';
	let showCreateModal = false;
	let showEditModal = false;
	let editingChallenge: any = null;
	let actionLoading = '';

	// Challenge creation
	let newChallenge = {
		name: '',
		description: '',
		category: '',
		difficulty: 'easy',
		base_points: 100,
		flag: '',
		flags: [{ name: 'User Flag', flag: '', points: 50 }, { name: 'Root Flag', flag: '', points: 50 }],
		type: 'container',
		docker_image: '',
		ova_url: '',
		files: []
	};
	let uploadLoading = false;
	let uploadError = '';
	let ovaFile: File | null = null;
	let uploadProgress = 0;

	const difficultyConfig: Record<string, { color: string; bg: string }> = {
		easy: { color: 'text-green-400', bg: 'bg-green-500/10' },
		medium: { color: 'text-yellow-400', bg: 'bg-yellow-500/10' },
		hard: { color: 'text-orange-400', bg: 'bg-orange-500/10' },
		insane: { color: 'text-red-400', bg: 'bg-red-500/10' }
	};

	function addFlag() {
		newChallenge.flags = [...newChallenge.flags, { name: `Flag ${newChallenge.flags.length + 1}`, flag: '', points: 25 }];
	}

	function removeFlag(index: number) {
		newChallenge.flags = newChallenge.flags.filter((_, i) => i !== index);
	}

	async function handleOvaUpload(event: Event) {
		const input = event.target as HTMLInputElement;
		if (input.files && input.files[0]) {
			ovaFile = input.files[0];
		}
	}

	async function publishChallenge(challenge: any) {
		actionLoading = challenge.id;
		try {
			await api.publishChallenge(challenge.id);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to publish');
		} finally {
			actionLoading = '';
		}
	}

	async function unpublishChallenge(challenge: any) {
		actionLoading = challenge.id;
		try {
			await api.unpublishChallenge(challenge.id);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to unpublish');
		} finally {
			actionLoading = '';
		}
	}

	async function deleteChallenge(challenge: any) {
		if (!confirm(`Delete "${challenge.name}"? This cannot be undone.`)) return;
		actionLoading = challenge.id;
		try {
			await api.deleteAdminChallenge(challenge.id);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to delete');
		} finally {
			actionLoading = '';
		}
	}

	function openEditModal(challenge: any) {
		editingChallenge = { ...challenge };
		showEditModal = true;
	}

	async function handleEditChallenge() {
		if (!editingChallenge) return;
		actionLoading = editingChallenge.id;
		try {
			await api.updateAdminChallenge(editingChallenge.id, {
				name: editingChallenge.name,
				description: editingChallenge.description,
				difficulty: editingChallenge.difficulty,
				base_points: editingChallenge.base_points
			});
			await loadDashboard();
			showEditModal = false;
			editingChallenge = null;
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to update');
		} finally {
			actionLoading = '';
		}
	}

	onMount(async () => {
		if (!$auth.isAuthenticated || $auth.user?.role !== 'admin') {
			goto('/');
			return;
		}
		await loadDashboard();
	});

	async function loadDashboard() {
		loading = true;
		try {
			const [statsRes, usersRes, challengesRes] = await Promise.all([
				api.getAdminStats(),
				api.getAdminUsers(),
				api.getAdminChallenges()
			]);
			stats = statsRes;
			users = usersRes.users || [];
			challenges = challengesRes.challenges || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load dashboard';
		} finally {
			loading = false;
		}
	}

	function formatDate(timestamp: number): string {
		return new Date(timestamp * 1000).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
	}

	async function handleCreateChallenge() {
		uploadLoading = true;
		uploadError = '';
		uploadProgress = 0;

		try {
			if (newChallenge.type === 'ova' && ovaFile) {
				// OVA upload using FormData
				const formData = new FormData();
				formData.append('file', ovaFile);
				formData.append('name', newChallenge.name);
				formData.append('description', newChallenge.description);
				formData.append('difficulty', newChallenge.difficulty);
				formData.append('base_points', String(newChallenge.base_points));
				formData.append('category', newChallenge.category);
				formData.append('flags', JSON.stringify(newChallenge.flags));

				await api.uploadOvaChallenge(formData, (progress) => {
					uploadProgress = progress;
				});
			} else {
				// Container challenge
				await api.createAdminChallenge({
					name: newChallenge.name,
					description: newChallenge.description,
					difficulty: newChallenge.difficulty,
					base_points: newChallenge.base_points,
					category: newChallenge.category,
					container_image: newChallenge.docker_image,
					flag: newChallenge.flag || newChallenge.flags[0]?.flag
				});
			}

			showCreateModal = false;
			await loadDashboard();
			
			// Reset form
			newChallenge = {
				name: '',
				description: '',
				category: '',
				difficulty: 'easy',
				base_points: 100,
				flag: '',
				flags: [{ name: 'User Flag', flag: '', points: 50 }, { name: 'Root Flag', flag: '', points: 50 }],
				type: 'container',
				docker_image: '',
				ova_url: '',
				files: []
			};
			ovaFile = null;
		} catch (e) {
			uploadError = e instanceof Error ? e.message : 'Failed to create challenge';
		} finally {
			uploadLoading = false;
		}
	}
</script>

<svelte:head>
	<title>Admin - Anvil</title>
</svelte:head>

<div class="min-h-screen bg-black">
	<div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<!-- Header -->
		<div class="flex items-center justify-between mb-8">
			<div>
				<h1 class="text-xl font-semibold text-white">Admin</h1>
				<p class="text-sm text-stone-500 mt-1">Platform management</p>
			</div>
			{#if activeTab === 'challenges'}
				<button
					on:click={() => showCreateModal = true}
					class="px-4 py-2 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition flex items-center gap-2"
				>
					<Icon icon="mdi:plus" class="w-4 h-4" />
					New Challenge
				</button>
			{/if}
		</div>

		{#if loading}
			<div class="flex items-center justify-center min-h-[40vh]">
				<Icon icon="mdi:loading" class="w-6 h-6 text-stone-600 animate-spin" />
			</div>
		{:else if error}
			<div class="text-center py-12">
				<p class="text-red-400 text-sm">{error}</p>
			</div>
		{:else}
			<!-- Tabs -->
			<div class="flex gap-1 mb-8 border-b border-stone-800">
				{#each [
					{ id: 'overview', label: 'Overview' },
					{ id: 'challenges', label: 'Challenges' },
					{ id: 'users', label: 'Users' }
				] as tab}
					<button
						type="button"
						on:click={() => activeTab = tab.id}
						class="px-4 py-2.5 text-sm font-medium transition-colors relative {activeTab === tab.id ? 'text-white' : 'text-stone-500 hover:text-stone-300'}"
					>
						{tab.label}
						{#if activeTab === tab.id}
							<div class="absolute bottom-0 left-0 right-0 h-0.5 bg-white"></div>
						{/if}
					</button>
				{/each}
			</div>

			<!-- Overview Tab -->
			{#if activeTab === 'overview'}
				<!-- Stats Grid -->
				<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
					{#each [
						{ label: 'Users', value: stats?.total_users || 0 },
						{ label: 'Challenges', value: stats?.total_challenges || 0 },
						{ label: 'Active Instances', value: stats?.active_instances || 0 },
						{ label: 'Total Solves', value: stats?.total_solves || 0 }
					] as stat}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
							<p class="text-xs text-stone-500 uppercase tracking-wider">{stat.label}</p>
							<p class="text-2xl font-semibold text-white mt-1">{stat.value}</p>
						</div>
					{/each}
				</div>

				<!-- Recent Activity -->
				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
					<!-- Recent Users -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Recent Users</h3>
						</div>
						<div class="divide-y divide-stone-800">
							{#each users.slice(0, 5) as user}
								<div class="px-4 py-3 flex items-center justify-between">
									<div class="flex items-center gap-3">
										<div class="w-8 h-8 bg-stone-800 rounded-full flex items-center justify-center">
											<span class="text-xs font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
										</div>
										<div>
											<p class="text-sm text-white">{user.username}</p>
											<p class="text-xs text-stone-500">{user.email}</p>
										</div>
									</div>
									<span class="text-xs text-stone-500">{formatDate(user.created_at)}</span>
								</div>
							{/each}
							{#if users.length === 0}
								<div class="px-4 py-8 text-center">
									<p class="text-sm text-stone-500">No users yet</p>
								</div>
							{/if}
						</div>
					</div>

					<!-- Top Challenges -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Top Challenges</h3>
						</div>
						<div class="divide-y divide-stone-800">
							{#each challenges.slice(0, 5) as challenge}
								<div class="px-4 py-3 flex items-center justify-between">
									<div>
										<p class="text-sm text-white">{challenge.name}</p>
										<p class="text-xs text-stone-500 capitalize">{challenge.difficulty}</p>
									</div>
									<span class="text-xs text-stone-400">{challenge.total_solves || 0} solves</span>
								</div>
							{/each}
							{#if challenges.length === 0}
								<div class="px-4 py-8 text-center">
									<p class="text-sm text-stone-500">No challenges yet</p>
								</div>
							{/if}
						</div>
					</div>
				</div>
			{/if}

			<!-- Challenges Tab -->
			{#if activeTab === 'challenges'}
				<!-- Mobile: Card view -->
				<div class="lg:hidden space-y-3">
					{#each challenges as challenge}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
							<div class="flex items-start justify-between mb-3">
								<div>
									<a href="/challenges/{challenge.slug}" class="text-sm font-medium text-white hover:text-stone-300 transition">{challenge.name}</a>
									<div class="flex items-center gap-2 mt-1">
										<span class="text-xs capitalize {difficultyConfig[challenge.difficulty]?.color}">{challenge.difficulty}</span>
										<span class="text-xs text-stone-600">•</span>
										<span class="text-xs text-stone-400">{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}</span>
									</div>
								</div>
								{#if challenge.status === 'published'}
									<span class="text-xs text-green-400">Published</span>
								{:else}
									<span class="text-xs text-yellow-400">Draft</span>
								{/if}
							</div>
							<div class="flex items-center justify-between text-xs text-stone-500 mb-3">
								<span>{challenge.base_points} pts</span>
								<span>{challenge.total_solves || 0} solves</span>
							</div>
							<div class="flex items-center gap-3 pt-3 border-t border-stone-800">
								<button
									on:click={() => openEditModal(challenge)}
									class="text-xs text-stone-400 hover:text-white transition"
									disabled={actionLoading === challenge.id}
								>
									Edit
								</button>
								{#if challenge.status === 'draft'}
									<button
										on:click={() => publishChallenge(challenge)}
										class="text-xs text-green-400 hover:text-green-300 transition"
										disabled={actionLoading === challenge.id}
									>
										Publish
									</button>
								{:else}
									<button
										on:click={() => unpublishChallenge(challenge)}
										class="text-xs text-yellow-400 hover:text-yellow-300 transition"
										disabled={actionLoading === challenge.id}
									>
										Unpublish
									</button>
								{/if}
								<button
									on:click={() => deleteChallenge(challenge)}
									class="text-xs text-red-400 hover:text-red-300 transition ml-auto"
									disabled={actionLoading === challenge.id}
								>
									Delete
								</button>
							</div>
						</div>
					{/each}
					{#if challenges.length === 0}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-8 text-center">
							<p class="text-sm text-stone-500">No challenges yet</p>
							<button
								on:click={() => showCreateModal = true}
								class="mt-3 text-sm text-white hover:text-stone-300 transition"
							>
								Create your first challenge →
							</button>
						</div>
					{/if}
				</div>

				<!-- Desktop: Table view -->
				<div class="hidden lg:block bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
					<table class="w-full">
						<thead>
							<tr class="border-b border-stone-800">
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Name</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Difficulty</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Type</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Points</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Solves</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Status</th>
								<th class="px-4 py-3 text-right text-xs font-medium text-stone-500 uppercase tracking-wider">Actions</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-stone-800">
							{#each challenges as challenge}
								<tr class="hover:bg-stone-900/50 transition-colors">
									<td class="px-4 py-3">
										<a href="/challenges/{challenge.slug}" class="text-sm text-white hover:text-stone-300 transition">{challenge.name}</a>
									</td>
									<td class="px-4 py-3">
										<span class="text-xs capitalize {difficultyConfig[challenge.difficulty]?.color}">{challenge.difficulty}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-xs text-stone-400">{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-sm text-white">{challenge.base_points}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-sm text-stone-400">{challenge.total_solves || 0}</span>
									</td>
									<td class="px-4 py-3">
										{#if challenge.status === 'published'}
											<span class="text-xs text-green-400">Published</span>
										{:else}
											<span class="text-xs text-yellow-400">Draft</span>
										{/if}
									</td>
									<td class="px-4 py-3 text-right">
										<div class="flex items-center justify-end gap-2">
											<button
												on:click={() => openEditModal(challenge)}
												class="text-xs text-stone-400 hover:text-white transition"
												disabled={actionLoading === challenge.id}
											>
												Edit
											</button>
											{#if challenge.status === 'draft'}
												<button
													on:click={() => publishChallenge(challenge)}
													class="text-xs text-green-400 hover:text-green-300 transition"
													disabled={actionLoading === challenge.id}
												>
													Publish
												</button>
											{:else}
												<button
													on:click={() => unpublishChallenge(challenge)}
													class="text-xs text-yellow-400 hover:text-yellow-300 transition"
													disabled={actionLoading === challenge.id}
												>
													Unpublish
												</button>
											{/if}
											<button
												on:click={() => deleteChallenge(challenge)}
												class="text-xs text-red-400 hover:text-red-300 transition"
												disabled={actionLoading === challenge.id}
											>
												Delete
											</button>
										</div>
									</td>
								</tr>
							{/each}
							{#if challenges.length === 0}
								<tr>
									<td colspan="7" class="px-4 py-12 text-center">
										<p class="text-sm text-stone-500">No challenges yet</p>
										<button
											on:click={() => showCreateModal = true}
											class="mt-3 text-sm text-white hover:text-stone-300 transition"
										>
											Create your first challenge →
										</button>
									</td>
								</tr>
							{/if}
						</tbody>
					</table>
				</div>
			{/if}

			<!-- Users Tab -->
			{#if activeTab === 'users'}
				<!-- Mobile: Card view -->
				<div class="lg:hidden space-y-3">
					{#each users as user}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
							<div class="flex items-center gap-3 mb-3">
								<div class="w-10 h-10 bg-stone-800 rounded-full flex items-center justify-center">
									<span class="text-sm font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
								</div>
								<div class="flex-1 min-w-0">
									<p class="text-sm font-medium text-white truncate">{user.username}</p>
									<p class="text-xs text-stone-500 truncate">{user.email}</p>
								</div>
								<span class="text-xs {user.role === 'admin' ? 'text-amber-400' : 'text-stone-400'}">{user.role}</span>
							</div>
							<div class="flex items-center justify-between text-xs text-stone-500 pt-3 border-t border-stone-800">
								<span>{user.total_score || 0} points</span>
								<span>Joined {formatDate(user.created_at)}</span>
							</div>
						</div>
					{/each}
					{#if users.length === 0}
						<div class="bg-stone-950 border border-stone-800 rounded-lg p-8 text-center">
							<p class="text-sm text-stone-500">No users yet</p>
						</div>
					{/if}
				</div>

				<!-- Desktop: Table view -->
				<div class="hidden lg:block bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
					<table class="w-full">
						<thead>
							<tr class="border-b border-stone-800">
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">User</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Email</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Role</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Score</th>
								<th class="px-4 py-3 text-left text-xs font-medium text-stone-500 uppercase tracking-wider">Joined</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-stone-800">
							{#each users as user}
								<tr class="hover:bg-stone-900/50 transition-colors">
									<td class="px-4 py-3">
										<div class="flex items-center gap-3">
											<div class="w-8 h-8 bg-stone-800 rounded-full flex items-center justify-center">
												<span class="text-xs font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
											</div>
											<span class="text-sm text-white">{user.username}</span>
										</div>
									</td>
									<td class="px-4 py-3">
										<span class="text-sm text-stone-400">{user.email}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-xs {user.role === 'admin' ? 'text-amber-400' : 'text-stone-400'}">{user.role}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-sm text-white">{user.total_score || 0}</span>
									</td>
									<td class="px-4 py-3">
										<span class="text-xs text-stone-500">{formatDate(user.created_at)}</span>
									</td>
								</tr>
							{/each}
							{#if users.length === 0}
								<tr>
									<td colspan="5" class="px-4 py-12 text-center">
										<p class="text-sm text-stone-500">No users yet</p>
									</td>
								</tr>
							{/if}
						</tbody>
					</table>
				</div>
			{/if}
		{/if}
	</div>
</div>

<!-- Create Challenge Modal -->
{#if showCreateModal}
	<div 
		class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4"
		on:click={() => showCreateModal = false}
		on:keydown={(e) => e.key === 'Escape' && (showCreateModal = false)}
		role="dialog"
		aria-modal="true"
	>
		<div 
			class="bg-stone-950 border border-stone-800 rounded-lg w-full max-w-lg max-h-[90vh] overflow-y-auto"
			on:click|stopPropagation
			on:keydown|stopPropagation
			role="document"
		>
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between">
				<h2 class="text-lg font-medium text-white">New Challenge</h2>
				<button on:click={() => showCreateModal = false} class="text-stone-400 hover:text-white transition">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={handleCreateChallenge} class="p-6 space-y-4">
				{#if uploadError}
					<div class="py-2 px-3 bg-red-500/10 border border-red-500/20 rounded text-red-400 text-sm">
						{uploadError}
					</div>
				{/if}

				{#if uploadLoading && uploadProgress > 0}
					<div>
						<div class="flex justify-between text-xs text-stone-400 mb-1">
							<span>Uploading...</span>
							<span>{uploadProgress}%</span>
						</div>
						<div class="w-full bg-stone-800 rounded-full h-1.5">
							<div class="bg-white h-full rounded-full transition-all" style="width: {uploadProgress}%"></div>
						</div>
					</div>
				{/if}

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Name</label>
					<input
						type="text"
						bind:value={newChallenge.name}
						required
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						placeholder="Challenge name"
					/>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Description</label>
					<textarea
						bind:value={newChallenge.description}
						rows="3"
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700 resize-none"
						placeholder="Challenge description"
					></textarea>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Difficulty</label>
						<select
							bind:value={newChallenge.difficulty}
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						>
							<option value="easy">Easy</option>
							<option value="medium">Medium</option>
							<option value="hard">Hard</option>
							<option value="insane">Insane</option>
						</select>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Points</label>
						<input
							type="number"
							bind:value={newChallenge.base_points}
							required
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Category</label>
					<input
						type="text"
						bind:value={newChallenge.category}
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						placeholder="e.g., Web, Pwn, Crypto"
					/>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Type</label>
					<div class="flex gap-2">
						<button
							type="button"
							on:click={() => newChallenge.type = 'container'}
							class="flex-1 py-2 text-sm rounded border transition {newChallenge.type === 'container' ? 'bg-white text-black border-white' : 'bg-transparent text-stone-400 border-stone-800 hover:border-stone-700'}"
						>
							Docker
						</button>
						<button
							type="button"
							on:click={() => newChallenge.type = 'ova'}
							class="flex-1 py-2 text-sm rounded border transition {newChallenge.type === 'ova' ? 'bg-white text-black border-white' : 'bg-transparent text-stone-400 border-stone-800 hover:border-stone-700'}"
						>
							VM (OVA)
						</button>
					</div>
				</div>

				{#if newChallenge.type === 'container'}
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Docker Image</label>
						<input
							type="text"
							bind:value={newChallenge.docker_image}
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm font-mono focus:outline-none focus:border-stone-700"
							placeholder="e.g., nginx:latest"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Flag</label>
						<input
							type="text"
							bind:value={newChallenge.flag}
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm font-mono focus:outline-none focus:border-stone-700"
							placeholder="flag&#123;...&#125;"
						/>
					</div>
				{:else}
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">OVA File</label>
						<div class="border border-dashed border-stone-800 rounded p-4 text-center">
							<input
								type="file"
								accept=".ova"
								on:change={handleOvaUpload}
								class="hidden"
								id="ova-upload"
							/>
							<label for="ova-upload" class="cursor-pointer">
								{#if ovaFile}
									<p class="text-sm text-white">{ovaFile.name}</p>
									<p class="text-xs text-stone-500 mt-1">{(ovaFile.size / 1024 / 1024).toFixed(2)} MB</p>
								{:else}
									<Icon icon="mdi:upload" class="w-8 h-8 text-stone-600 mx-auto mb-2" />
									<p class="text-sm text-stone-400">Click to upload OVA</p>
								{/if}
							</label>
						</div>
					</div>

					<!-- Flags for OVA -->
					<div>
						<div class="flex items-center justify-between mb-2">
							<label class="text-xs text-stone-500">Flags</label>
							<button type="button" on:click={addFlag} class="text-xs text-stone-400 hover:text-white transition">
								+ Add Flag
							</button>
						</div>
						<div class="space-y-2">
							{#each newChallenge.flags as flag, i}
								<div class="flex gap-2">
									<input
										type="text"
										bind:value={flag.name}
										placeholder="Name"
										class="w-24 px-2 py-1.5 bg-black border border-stone-800 rounded text-white text-xs focus:outline-none focus:border-stone-700"
									/>
									<input
										type="text"
										bind:value={flag.flag}
										placeholder="flag&#123;...&#125;"
										class="flex-1 px-2 py-1.5 bg-black border border-stone-800 rounded text-white text-xs font-mono focus:outline-none focus:border-stone-700"
									/>
									<input
										type="number"
										bind:value={flag.points}
										placeholder="Pts"
										class="w-16 px-2 py-1.5 bg-black border border-stone-800 rounded text-white text-xs focus:outline-none focus:border-stone-700"
									/>
									{#if newChallenge.flags.length > 1}
										<button type="button" on:click={() => removeFlag(i)} class="text-red-400 hover:text-red-300 transition">
											<Icon icon="mdi:close" class="w-4 h-4" />
										</button>
									{/if}
								</div>
							{/each}
						</div>
					</div>
				{/if}

				<div class="flex gap-3 pt-2">
					<button
						type="submit"
						disabled={uploadLoading}
						class="flex-1 py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition disabled:opacity-50"
					>
						{#if uploadLoading}
							{uploadProgress > 0 ? `Uploading ${uploadProgress}%` : 'Creating...'}
						{:else}
							Create
						{/if}
					</button>
					<button
						type="button"
						on:click={() => showCreateModal = false}
						disabled={uploadLoading}
						class="px-4 py-2.5 text-stone-400 text-sm hover:text-white transition disabled:opacity-50"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}

<!-- Edit Challenge Modal -->
{#if showEditModal && editingChallenge}
	<div 
		class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4"
		on:click={() => { showEditModal = false; editingChallenge = null; }}
		on:keydown={(e) => e.key === 'Escape' && (showEditModal = false, editingChallenge = null)}
		role="dialog"
		aria-modal="true"
	>
		<div 
			class="bg-stone-950 border border-stone-800 rounded-lg w-full max-w-lg"
			on:click|stopPropagation
			on:keydown|stopPropagation
			role="document"
		>
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between">
				<h2 class="text-lg font-medium text-white">Edit Challenge</h2>
				<button on:click={() => { showEditModal = false; editingChallenge = null; }} class="text-stone-400 hover:text-white transition">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={handleEditChallenge} class="p-6 space-y-4">
				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Name</label>
					<input
						type="text"
						bind:value={editingChallenge.name}
						required
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
					/>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Description</label>
					<textarea
						bind:value={editingChallenge.description}
						rows="3"
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700 resize-none"
					></textarea>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Difficulty</label>
						<select
							bind:value={editingChallenge.difficulty}
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						>
							<option value="easy">Easy</option>
							<option value="medium">Medium</option>
							<option value="hard">Hard</option>
							<option value="insane">Insane</option>
						</select>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Points</label>
						<input
							type="number"
							bind:value={editingChallenge.base_points}
							required
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
				</div>

				<div class="py-3 px-4 bg-stone-900/50 rounded border border-stone-800">
					<div class="flex items-center justify-between">
						<div>
							<p class="text-sm text-white">{editingChallenge.name}</p>
							<p class="text-xs text-stone-500">Type: {editingChallenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker'}</p>
						</div>
						<span class="text-xs {editingChallenge.status === 'published' ? 'text-green-400' : 'text-yellow-400'}">
							{editingChallenge.status}
						</span>
					</div>
				</div>

				<div class="flex gap-3 pt-2">
					<button
						type="submit"
						disabled={actionLoading === editingChallenge.id}
						class="flex-1 py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition disabled:opacity-50"
					>
						{actionLoading === editingChallenge.id ? 'Saving...' : 'Save'}
					</button>
					<button
						type="button"
						on:click={() => { showEditModal = false; editingChallenge = null; }}
						class="px-4 py-2.5 text-stone-400 text-sm hover:text-white transition"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}
