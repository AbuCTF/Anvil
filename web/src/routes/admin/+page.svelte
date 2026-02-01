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

	// Infrastructure data
	let infraStats: any = null;
	let nodes: any[] = [];
	let templates: any[] = [];
	let activeInstances: any[] = [];
	let showNodeModal = false;
	let showTemplateUploadModal = false;

	// New node form
	let newNode = {
		name: '',
		hostname: '',
		ip_address: '',
		total_vcpu: 16,
		total_memory_mb: 61440,
		total_disk_gb: 100,
		max_vms: 10,
		region: '',
		provider: 'gcp'
	};

	// Template upload
	let templateFile: File | null = null;
	let templateName = '';
	let templateDescription = '';
	let templateMinVcpu = 2;
	let templateMinMemory = 2048;
	let templateUploadProgress = 0;
	let templateUploading = false;

	// Platform settings
	let platformSettings: Record<string, any> = {};
	let savingSettings = false;
	let settingsChanged = false;

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
		files: [],
		// Timer settings
		vm_timeout_minutes: 60,
		vm_max_extensions: 2,
		vm_extension_minutes: 30,
		cooldown_minutes: 15
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

			// Load infrastructure data in parallel
			try {
				const [infraRes, nodesRes, templatesRes, instancesRes] = await Promise.all([
					api.getInfrastructureStats(),
					api.getNodes(),
					api.getVMTemplates(),
					api.getActiveInstances()
				]);
				infraStats = infraRes;
				nodes = nodesRes.nodes || [];
				templates = templatesRes.templates || [];
				activeInstances = instancesRes.instances || [];
			} catch {
				// Infrastructure endpoints may not be available yet
				infraStats = null;
				nodes = [];
				templates = [];
				activeInstances = [];
			}

			// Load platform settings
			try {
				const settingsRes = await api.getPlatformSettings();
				platformSettings = settingsRes.settings || {};
			} catch {
				platformSettings = {};
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load dashboard';
		} finally {
			loading = false;
		}
	}

	async function savePlatformSettings() {
		savingSettings = true;
		try {
			await api.updatePlatformSettings(platformSettings);
			settingsChanged = false;
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to save settings');
		} finally {
			savingSettings = false;
		}
	}

	function updateSetting(key: string, value: any) {
		platformSettings = { ...platformSettings, [key]: value };
		settingsChanged = true;
	}

	async function createNode() {
		actionLoading = 'create-node';
		try {
			await api.createNode(newNode);
			showNodeModal = false;
			newNode = { name: '', hostname: '', ip_address: '', total_vcpu: 16, total_memory_mb: 61440, total_disk_gb: 100, max_vms: 10, region: '', provider: 'gcp' };
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to create node');
		} finally {
			actionLoading = '';
		}
	}

	async function deleteNode(nodeId: string) {
		if (!confirm('Delete this node? This cannot be undone.')) return;
		actionLoading = nodeId;
		try {
			await api.deleteNode(nodeId);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to delete node');
		} finally {
			actionLoading = '';
		}
	}

	async function uploadTemplate() {
		if (!templateFile || !templateName) return;
		templateUploading = true;
		templateUploadProgress = 0;
		try {
			const formData = new FormData();
			formData.append('file', templateFile);
			formData.append('name', templateName);
			formData.append('description', templateDescription);
			formData.append('min_vcpu', String(templateMinVcpu));
			formData.append('min_memory_mb', String(templateMinMemory));
			formData.append('os_type', 'linux');

			await api.uploadVMTemplate(formData, (progress) => {
				templateUploadProgress = progress;
			});

			showTemplateUploadModal = false;
			templateFile = null;
			templateName = '';
			templateDescription = '';
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to upload template');
		} finally {
			templateUploading = false;
		}
	}

	async function deleteTemplate(templateId: string) {
		if (!confirm('Delete this template? Challenges using it will break.')) return;
		actionLoading = templateId;
		try {
			await api.deleteVMTemplate(templateId);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to delete template');
		} finally {
			actionLoading = '';
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
			{:else if activeTab === 'infrastructure'}
				<div class="flex gap-2">
					<button
						on:click={() => showTemplateUploadModal = true}
						class="px-4 py-2 bg-stone-800 text-white text-sm font-medium rounded hover:bg-stone-700 transition flex items-center gap-2"
					>
						<Icon icon="mdi:upload" class="w-4 h-4" />
						Upload Template
					</button>
					<button
						on:click={() => showNodeModal = true}
						class="px-4 py-2 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition flex items-center gap-2"
					>
						<Icon icon="mdi:plus" class="w-4 h-4" />
						Add Node
					</button>
				</div>
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
					{ id: 'overview', label: 'Dashboard', icon: 'mdi:view-dashboard' },
					{ id: 'challenges', label: 'Challenges', icon: 'mdi:flag-variant' },
					{ id: 'users', label: 'Users', icon: 'mdi:account-group' },
					{ id: 'infrastructure', label: 'System', icon: 'mdi:server-network' },
					{ id: 'settings', label: 'Settings', icon: 'mdi:cog' }
				] as tab}
					<button
						type="button"
						on:click={() => activeTab = tab.id}
						class="px-4 py-2.5 text-sm font-medium transition-colors relative flex items-center gap-2 {activeTab === tab.id ? 'text-white' : 'text-stone-500 hover:text-stone-300'}"
					>
						<Icon icon={tab.icon} class="w-4 h-4" />
						{tab.label}
						{#if activeTab === tab.id}
							<div class="absolute bottom-0 left-0 right-0 h-0.5 bg-white"></div>
						{/if}
					</button>
				{/each}
			</div>

			<!-- Dashboard Tab -->
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
							<div class="flex items-center gap-2 pt-3 border-t border-stone-800">
								<button
									on:click={() => openEditModal(challenge)}
									class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-stone-300 bg-stone-800 hover:bg-stone-700 rounded-lg transition disabled:opacity-50"
									disabled={actionLoading === challenge.id}
									title="Edit challenge"
								>
									<Icon icon="mdi:pencil" class="w-3.5 h-3.5" />
									Edit
								</button>
								{#if challenge.status === 'draft'}
									<button
										on:click={() => publishChallenge(challenge)}
										class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-green-400 bg-green-500/10 hover:bg-green-500/20 border border-green-500/20 rounded-lg transition disabled:opacity-50"
										disabled={actionLoading === challenge.id}
										title="Publish challenge"
									>
										<Icon icon="mdi:rocket-launch" class="w-3.5 h-3.5" />
										Publish
									</button>
								{:else}
									<button
										on:click={() => unpublishChallenge(challenge)}
										class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-yellow-400 bg-yellow-500/10 hover:bg-yellow-500/20 border border-yellow-500/20 rounded-lg transition disabled:opacity-50"
										disabled={actionLoading === challenge.id}
										title="Unpublish challenge"
									>
										<Icon icon="mdi:eye-off" class="w-3.5 h-3.5" />
										Unpublish
									</button>
								{/if}
								<button
									on:click={() => deleteChallenge(challenge)}
									class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-red-400 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 rounded-lg transition disabled:opacity-50 ml-auto"
									disabled={actionLoading === challenge.id}
									title="Delete challenge"
								>
									<Icon icon="mdi:trash-can" class="w-3.5 h-3.5" />
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
										<div class="flex items-center justify-end gap-1.5">
											<button
												on:click={() => openEditModal(challenge)}
												class="p-2 text-stone-400 hover:text-white hover:bg-stone-800 rounded-lg transition disabled:opacity-50"
												disabled={actionLoading === challenge.id}
												title="Edit"
											>
												<Icon icon="mdi:pencil" class="w-4 h-4" />
											</button>
											{#if challenge.status === 'draft'}
												<button
													on:click={() => publishChallenge(challenge)}
													class="p-2 text-green-400 hover:text-green-300 hover:bg-green-500/10 rounded-lg transition disabled:opacity-50"
													disabled={actionLoading === challenge.id}
													title="Publish"
												>
													<Icon icon="mdi:rocket-launch" class="w-4 h-4" />
												</button>
											{:else}
												<button
													on:click={() => unpublishChallenge(challenge)}
													class="p-2 text-yellow-400 hover:text-yellow-300 hover:bg-yellow-500/10 rounded-lg transition disabled:opacity-50"
													disabled={actionLoading === challenge.id}
													title="Unpublish"
												>
													<Icon icon="mdi:eye-off" class="w-4 h-4" />
												</button>
											{/if}
											<button
												on:click={() => deleteChallenge(challenge)}
												class="p-2 text-red-400 hover:text-red-300 hover:bg-red-500/10 rounded-lg transition disabled:opacity-50"
												disabled={actionLoading === challenge.id}
												title="Delete"
											>
												<Icon icon="mdi:trash-can" class="w-4 h-4" />
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

			<!-- Infrastructure Tab -->
			{#if activeTab === 'infrastructure'}
				<!-- Stats Cards -->
				<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
					<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wider">Nodes</p>
						<p class="text-2xl font-semibold text-white mt-1">
							{infraStats?.nodes?.online || 0}/{infraStats?.nodes?.total || 0}
						</p>
						<p class="text-xs text-green-400 mt-1">online</p>
					</div>
					<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wider">vCPU</p>
						<p class="text-2xl font-semibold text-white mt-1">
							{infraStats?.resources?.vcpu?.used || 0}/{infraStats?.resources?.vcpu?.total || 0}
						</p>
						<p class="text-xs text-stone-400 mt-1">{infraStats?.resources?.vcpu?.available || 0} available</p>
					</div>
					<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wider">Memory</p>
						<p class="text-2xl font-semibold text-white mt-1">
							{infraStats?.resources?.memory_gb?.used || 0}/{infraStats?.resources?.memory_gb?.total || 0} GB
						</p>
						<p class="text-xs text-stone-400 mt-1">{infraStats?.resources?.memory_gb?.available || 0} GB free</p>
					</div>
					<div class="bg-stone-950 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wider">Running VMs</p>
						<p class="text-2xl font-semibold text-white mt-1">{infraStats?.vms?.running || 0}</p>
						<p class="text-xs text-stone-400 mt-1">of {infraStats?.vms?.total || 0} total</p>
					</div>
				</div>

				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
					<!-- Nodes Section -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between">
							<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">VM Nodes</h3>
							<span class="text-xs text-stone-400">{nodes.length} nodes</span>
						</div>
						<div class="divide-y divide-stone-800">
							{#each nodes as node}
								<div class="px-4 py-3">
									<div class="flex items-center justify-between mb-2">
										<div class="flex items-center gap-2">
											<div class="w-2 h-2 rounded-full {node.status === 'online' ? 'bg-green-400' : 'bg-red-400'}"></div>
											<span class="text-sm text-white font-medium">{node.name}</span>
											{#if node.is_primary}
												<span class="text-xs bg-amber-500/20 text-amber-400 px-1.5 py-0.5 rounded">primary</span>
											{/if}
										</div>
										<button
											on:click={() => deleteNode(node.id)}
											disabled={actionLoading === node.id}
											class="text-stone-500 hover:text-red-400 transition disabled:opacity-50"
										>
											<Icon icon="mdi:trash-can" class="w-4 h-4" />
										</button>
									</div>
									<div class="text-xs text-stone-500">
										<span>{node.ip_address}</span>
										<span class="mx-2">•</span>
										<span>{node.active_vms}/{node.max_vms} VMs</span>
										<span class="mx-2">•</span>
										<span>{node.used_vcpu}/{node.total_vcpu} vCPU</span>
									</div>
									<!-- Resource bars -->
									<div class="mt-2 space-y-1">
										<div class="flex items-center gap-2">
											<span class="text-xs text-stone-600 w-12">CPU</span>
											<div class="flex-1 h-1.5 bg-stone-800 rounded-full overflow-hidden">
												<div 
													class="h-full bg-blue-500 transition-all"
													style="width: {node.total_vcpu ? (node.used_vcpu / node.total_vcpu * 100) : 0}%"
												></div>
											</div>
										</div>
										<div class="flex items-center gap-2">
											<span class="text-xs text-stone-600 w-12">RAM</span>
											<div class="flex-1 h-1.5 bg-stone-800 rounded-full overflow-hidden">
												<div 
													class="h-full bg-purple-500 transition-all"
													style="width: {node.total_memory_mb ? (node.used_memory_mb / node.total_memory_mb * 100) : 0}%"
												></div>
											</div>
										</div>
									</div>
								</div>
							{/each}
							{#if nodes.length === 0}
								<div class="px-4 py-8 text-center">
									<Icon icon="mdi:server-off" class="w-8 h-8 text-stone-700 mx-auto mb-2" />
									<p class="text-sm text-stone-500">No nodes configured</p>
									<button
										on:click={() => showNodeModal = true}
										class="mt-2 text-sm text-white hover:text-stone-300 transition"
									>
										Add your first node →
									</button>
								</div>
							{/if}
						</div>
					</div>

					<!-- VM Templates Section -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between">
							<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">VM Templates</h3>
							<span class="text-xs text-stone-400">{templates.length} templates</span>
						</div>
						<div class="divide-y divide-stone-800">
							{#each templates as template}
								<div class="px-4 py-3">
									<div class="flex items-center justify-between mb-1">
										<span class="text-sm text-white font-medium">{template.name}</span>
										<div class="flex items-center gap-2">
											{#if template.is_active}
												<span class="text-xs text-green-400">Active</span>
											{:else}
												<span class="text-xs text-stone-500">Inactive</span>
											{/if}
											<button
												on:click={() => deleteTemplate(template.id)}
												disabled={actionLoading === template.id}
												class="text-stone-500 hover:text-red-400 transition disabled:opacity-50"
											>
												<Icon icon="mdi:trash-can" class="w-4 h-4" />
											</button>
										</div>
									</div>
									<div class="text-xs text-stone-500">
										<span>{template.disk_size_gb?.toFixed(1) || '?'} GB</span>
										<span class="mx-2">•</span>
										<span>Min: {template.min_vcpu} vCPU / {template.min_memory_mb} MB</span>
									</div>
									{#if template.description}
										<p class="text-xs text-stone-600 mt-1 truncate">{template.description}</p>
									{/if}
								</div>
							{/each}
							{#if templates.length === 0}
								<div class="px-4 py-8 text-center">
									<Icon icon="mdi:harddisk" class="w-8 h-8 text-stone-700 mx-auto mb-2" />
									<p class="text-sm text-stone-500">No VM templates</p>
									<button
										on:click={() => showTemplateUploadModal = true}
										class="mt-2 text-sm text-white hover:text-stone-300 transition"
									>
										Upload your first template →
									</button>
								</div>
							{/if}
						</div>
					</div>
				</div>

				<!-- Active Instances -->
				<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
					<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between">
						<h3 class="text-xs font-medium text-stone-500 uppercase tracking-wider">Active VM Instances</h3>
						<span class="text-xs text-stone-400">{activeInstances.length} running</span>
					</div>
					{#if activeInstances.length > 0}
						<table class="w-full">
							<thead>
								<tr class="border-b border-stone-800">
									<th class="px-4 py-2 text-left text-xs font-medium text-stone-500 uppercase">User</th>
									<th class="px-4 py-2 text-left text-xs font-medium text-stone-500 uppercase">Challenge</th>
									<th class="px-4 py-2 text-left text-xs font-medium text-stone-500 uppercase">IP</th>
									<th class="px-4 py-2 text-left text-xs font-medium text-stone-500 uppercase">Status</th>
									<th class="px-4 py-2 text-left text-xs font-medium text-stone-500 uppercase">Expires</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-stone-800">
								{#each activeInstances as instance}
									<tr class="hover:bg-stone-900/50">
										<td class="px-4 py-2 text-sm text-white">{instance.username}</td>
										<td class="px-4 py-2 text-sm text-stone-300">{instance.challenge_name}</td>
										<td class="px-4 py-2 text-sm text-stone-400 font-mono">{instance.ip_address || '-'}</td>
										<td class="px-4 py-2">
											<span class="text-xs {instance.status === 'running' ? 'text-green-400' : 'text-yellow-400'}">{instance.status}</span>
										</td>
										<td class="px-4 py-2 text-xs text-stone-500">
											{instance.expires_at ? new Date(instance.expires_at * 1000).toLocaleTimeString() : '-'}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{:else}
						<div class="px-4 py-8 text-center">
							<Icon icon="mdi:desktop-mac-dashboard" class="w-8 h-8 text-stone-700 mx-auto mb-2" />
							<p class="text-sm text-stone-500">No active VM instances</p>
						</div>
					{/if}
				</div>
			{/if}

			<!-- Settings Tab -->
			{#if activeTab === 'settings'}
				<div class="space-y-6">
					<!-- Save Button -->
					{#if settingsChanged}
						<div class="flex justify-end">
							<button
								on:click={savePlatformSettings}
								disabled={savingSettings}
								class="flex items-center gap-2 px-4 py-2 bg-white text-black font-medium rounded-lg hover:bg-stone-200 transition disabled:opacity-50"
							>
								{#if savingSettings}
									<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" />
								{:else}
									<Icon icon="mdi:content-save" class="w-4 h-4" />
								{/if}
								Save Settings
							</button>
						</div>
					{/if}

					<!-- Instance Timeouts -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:timer-outline" class="w-4 h-4 text-stone-400" />
								Instance Timeouts
							</h3>
							<p class="text-xs text-stone-500 mt-1">Default session durations for VM instances by difficulty</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
								{#each [
									{ key: 'vm_timeout_easy', label: 'Easy', default: 90 },
									{ key: 'vm_timeout_medium', label: 'Medium', default: 120 },
									{ key: 'vm_timeout_hard', label: 'Hard', default: 180 },
									{ key: 'vm_timeout_insane', label: 'Insane', default: 240 }
								] as setting}
									<div>
										<label class="block text-xs font-medium text-stone-400 mb-1">{setting.label}</label>
										<div class="flex items-center gap-2">
											<input
												type="number"
												value={platformSettings[setting.key] || setting.default}
												on:input={(e) => updateSetting(setting.key, parseInt((e.target as HTMLInputElement).value))}
												min="30"
												max="480"
												class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
											/>
											<span class="text-xs text-stone-500">min</span>
										</div>
									</div>
								{/each}
							</div>
						</div>
					</div>

					<!-- Cooldown Settings -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:timer-sand" class="w-4 h-4 text-stone-400" />
								Cooldown Periods
							</h3>
							<p class="text-xs text-stone-500 mt-1">Wait time before users can restart an instance after stopping</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
								{#each [
									{ key: 'cooldown_easy', label: 'Easy', default: 5 },
									{ key: 'cooldown_medium', label: 'Medium', default: 10 },
									{ key: 'cooldown_hard', label: 'Hard', default: 15 },
									{ key: 'cooldown_insane', label: 'Insane', default: 20 }
								] as setting}
									<div>
										<label class="block text-xs font-medium text-stone-400 mb-1">{setting.label}</label>
										<div class="flex items-center gap-2">
											<input
												type="number"
												value={platformSettings[setting.key] || setting.default}
												on:input={(e) => updateSetting(setting.key, parseInt((e.target as HTMLInputElement).value))}
												min="0"
												max="120"
												class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
											/>
											<span class="text-xs text-stone-500">min</span>
										</div>
									</div>
								{/each}
							</div>
						</div>
					</div>

					<!-- Extension Settings -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:clock-plus-outline" class="w-4 h-4 text-stone-400" />
								Extension Settings
							</h3>
							<p class="text-xs text-stone-500 mt-1">How many times and by how much users can extend their session</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 gap-4">
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Max Extensions</label>
									<input
										type="number"
										value={platformSettings.max_extensions || 3}
										on:input={(e) => updateSetting('max_extensions', parseInt((e.target as HTMLInputElement).value))}
										min="0"
										max="10"
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									/>
								</div>
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Extension Duration</label>
									<div class="flex items-center gap-2">
										<input
											type="number"
											value={platformSettings.extension_minutes || 30}
											on:input={(e) => updateSetting('extension_minutes', parseInt((e.target as HTMLInputElement).value))}
											min="15"
											max="120"
											class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
										/>
										<span class="text-xs text-stone-500">min</span>
									</div>
								</div>
							</div>
						</div>
					</div>

					<!-- User Limits -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:account-multiple" class="w-4 h-4 text-stone-400" />
								User Limits
							</h3>
							<p class="text-xs text-stone-500 mt-1">Resource limits per user</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 gap-4">
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Max Concurrent Instances</label>
									<input
										type="number"
										value={platformSettings.max_instances_per_user || 1}
										on:input={(e) => updateSetting('max_instances_per_user', parseInt((e.target as HTMLInputElement).value))}
										min="1"
										max="5"
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									/>
								</div>
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Max Daily Submissions</label>
									<input
										type="number"
										value={platformSettings.max_daily_submissions || 100}
										on:input={(e) => updateSetting('max_daily_submissions', parseInt((e.target as HTMLInputElement).value))}
										min="10"
										max="1000"
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									/>
								</div>
							</div>
						</div>
					</div>

					<!-- VPN Settings -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:vpn" class="w-4 h-4 text-stone-400" />
								VPN Settings
							</h3>
							<p class="text-xs text-stone-500 mt-1">WireGuard VPN configuration</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 gap-4">
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">VPN Server Endpoint</label>
									<input
										type="text"
										value={platformSettings.vpn_endpoint || 'play.h7tex.com:51820'}
										on:input={(e) => updateSetting('vpn_endpoint', (e.target as HTMLInputElement).value)}
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									/>
								</div>
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Require VPN for Instances</label>
									<select
										value={platformSettings.require_vpn || 'true'}
										on:change={(e) => updateSetting('require_vpn', (e.target as HTMLSelectElement).value)}
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									>
										<option value="true">Yes</option>
										<option value="false">No</option>
									</select>
								</div>
							</div>
						</div>
					</div>

					<!-- Platform Settings -->
					<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
						<div class="px-4 py-3 border-b border-stone-800">
							<h3 class="text-sm font-medium text-white flex items-center gap-2">
								<Icon icon="mdi:cog" class="w-4 h-4 text-stone-400" />
								Platform Settings
							</h3>
							<p class="text-xs text-stone-500 mt-1">General platform configuration</p>
						</div>
						<div class="p-4 space-y-4">
							<div class="grid grid-cols-2 gap-4">
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Allow Registration</label>
									<select
										value={platformSettings.registration_enabled || 'true'}
										on:change={(e) => updateSetting('registration_enabled', (e.target as HTMLSelectElement).value)}
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									>
										<option value="true">Open</option>
										<option value="false">Closed</option>
									</select>
								</div>
								<div>
									<label class="block text-xs font-medium text-stone-400 mb-1">Scoreboard</label>
									<select
										value={platformSettings.scoreboard_enabled || 'true'}
										on:change={(e) => updateSetting('scoreboard_enabled', (e.target as HTMLSelectElement).value)}
										class="w-full px-3 py-2 bg-black border border-stone-700 rounded text-white text-sm focus:outline-none focus:border-stone-500"
									>
										<option value="true">Public</option>
										<option value="false">Hidden</option>
									</select>
								</div>
							</div>
						</div>
					</div>
				</div>
			{/if}
		{/if}
	</div>
</div>

<!-- Create Challenge Modal -->
{#if showCreateModal}
	<!-- svelte-ignore a11y-click-events-have-key-events -->
	<!-- svelte-ignore a11y-no-static-element-interactions -->
	<div 
		class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4"
		on:click={() => showCreateModal = false}
		role="dialog"
		aria-modal="true"
	>
		<!-- svelte-ignore a11y-click-events-have-key-events -->
		<!-- svelte-ignore a11y-no-static-element-interactions -->
		<div 
			class="bg-stone-950 border border-stone-800 rounded-2xl w-full max-w-2xl max-h-[90vh] flex flex-col shadow-2xl"
			on:click|stopPropagation
		>
			<!-- Header with Type Tabs -->
			<div class="p-6 border-b border-stone-800 flex-shrink-0">
				<div class="flex items-center justify-between mb-4">
					<h2 class="text-xl font-bold text-white flex items-center gap-2">
						<Icon icon="mdi:plus-circle" class="w-6 h-6" />
						Create Challenge
					</h2>
					<button on:click={() => showCreateModal = false} class="text-stone-400 hover:text-white transition p-1">
						<Icon icon="mdi:close" class="w-5 h-5" />
					</button>
				</div>
				
				<!-- Type Tabs at Top -->
				<div class="flex gap-2 p-1 bg-black rounded-xl">
					<button
						type="button"
						on:click={() => newChallenge.type = 'container'}
						class="flex-1 flex items-center justify-center gap-2 py-3 rounded-lg text-sm font-medium transition-all {newChallenge.type === 'container' ? 'bg-white text-black shadow-lg' : 'text-stone-400 hover:text-white'}"
					>
						<Icon icon="mdi:docker" class="w-5 h-5" />
						Docker Container
					</button>
					<button
						type="button"
						on:click={() => newChallenge.type = 'ova'}
						class="flex-1 flex items-center justify-center gap-2 py-3 rounded-lg text-sm font-medium transition-all {newChallenge.type === 'ova' ? 'bg-white text-black shadow-lg' : 'text-stone-400 hover:text-white'}"
					>
						<Icon icon="mdi:desktop-classic" class="w-5 h-5" />
						VM (OVA)
					</button>
				</div>
			</div>

			<!-- Scrollable Form Content -->
			<div class="overflow-y-auto flex-1 min-h-0">
				{#if uploadLoading && uploadProgress > 0}
					<div class="px-6 py-3 bg-stone-900/50 border-b border-stone-800">
						<div class="flex items-center justify-between mb-2">
							<span class="text-sm text-stone-300 font-medium">Uploading OVA...</span>
							<span class="text-sm text-stone-400">{uploadProgress}%</span>
						</div>
						<div class="w-full bg-stone-800 rounded-full h-2 overflow-hidden">
							<div class="bg-gradient-to-r from-green-500 to-emerald-400 h-full transition-all duration-300" style="width: {uploadProgress}%"></div>
						</div>
					</div>
				{/if}

				<form on:submit|preventDefault={handleCreateChallenge} class="p-6 space-y-5">
					{#if uploadError}
						<div class="flex items-center gap-2 py-3 px-4 bg-red-500/10 border border-red-500/20 rounded-xl text-red-400 text-sm">
							<Icon icon="mdi:alert-circle" class="w-5 h-5 flex-shrink-0" />
							{uploadError}
						</div>
					{/if}

					<!-- Basic Info -->
					<div class="grid grid-cols-1 md:grid-cols-2 gap-5">
						<div class="md:col-span-2">
							<label class="block text-sm font-medium text-stone-300 mb-2">Challenge Name *</label>
							<input
								type="text"
								bind:value={newChallenge.name}
								required
								class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
								placeholder="Enter challenge name"
							/>
						</div>

						<div>
							<label class="block text-sm font-medium text-stone-300 mb-2">Category *</label>
							<input
								type="text"
								bind:value={newChallenge.category}
								required
								class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
								placeholder="Web, Crypto, Pwn..."
							/>
						</div>

						<div>
							<label class="block text-sm font-medium text-stone-300 mb-2">Difficulty *</label>
							<select
								bind:value={newChallenge.difficulty}
								class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
							>
								<option value="easy">Easy</option>
								<option value="medium">Medium</option>
								<option value="hard">Hard</option>
								<option value="insane">Insane</option>
							</select>
						</div>

						<div>
							<label class="block text-sm font-medium text-stone-300 mb-2">Base Points *</label>
							<input
								type="number"
								bind:value={newChallenge.base_points}
								required
								min="1"
								class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
							/>
						</div>

						<div class="md:col-span-2">
							<label class="block text-sm font-medium text-stone-300 mb-2">Description *</label>
							<textarea
								bind:value={newChallenge.description}
								required
								rows="3"
								class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all resize-none"
								placeholder="Challenge description..."
							></textarea>
						</div>
					</div>

					<!-- Type-specific fields -->
					{#if newChallenge.type === 'container'}
						<div class="pt-4 border-t border-stone-800 space-y-5">
							<div>
								<label class="block text-sm font-medium text-stone-300 mb-2">Docker Image *</label>
								<input
									type="text"
									bind:value={newChallenge.docker_image}
									required
									class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white font-mono placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
									placeholder="registry.example.com/challenge:latest"
								/>
								<p class="text-stone-500 text-xs mt-2">Image should be pre-built and pushed to a registry</p>
							</div>
							<div>
								<label class="block text-sm font-medium text-stone-300 mb-2">Flag *</label>
								<input
									type="text"
									bind:value={newChallenge.flag}
									required
									class="w-full px-4 py-3 bg-black border border-stone-700 rounded-xl text-white font-mono placeholder-stone-500 focus:outline-none focus:border-stone-500 focus:ring-2 focus:ring-stone-500/20 transition-all"
									placeholder="flag&#123;example_flag_here&#125;"
								/>
							</div>
						</div>
					{:else}
						<div class="pt-4 border-t border-stone-800 space-y-5">
							<!-- OVA Upload -->
							<div>
								<label class="block text-sm font-medium text-stone-300 mb-2">OVA File *</label>
								<div class="border-2 border-dashed border-stone-700 rounded-xl p-6 text-center hover:border-stone-500 transition-colors">
									{#if ovaFile}
										<div class="flex items-center justify-center gap-3">
											<Icon icon="mdi:file-check" class="w-8 h-8 text-green-400" />
											<div class="text-left">
												<p class="text-white font-medium">{ovaFile.name}</p>
												<p class="text-stone-500 text-sm">{(ovaFile.size / 1024 / 1024 / 1024).toFixed(2)} GB</p>
											</div>
											<button type="button" on:click={() => ovaFile = null} class="text-red-400 hover:text-red-300 p-2">
												<Icon icon="mdi:close" class="w-5 h-5" />
											</button>
										</div>
									{:else}
										<Icon icon="mdi:cloud-upload" class="w-12 h-12 text-stone-500 mx-auto mb-3" />
										<p class="text-stone-400 mb-2">Drop your OVA file here or click to browse</p>
										<input type="file" accept=".ova,.qcow2,.vmdk" on:change={handleOvaUpload} class="hidden" id="ova-upload" />
										<label for="ova-upload" class="inline-block px-4 py-2 bg-stone-800 text-white rounded-lg cursor-pointer hover:bg-stone-700 transition-colors text-sm">
											Select File
										</label>
									{/if}
								</div>
								<p class="text-stone-500 text-xs mt-2">Supported: .ova, .qcow2, .vmdk (max 20GB)</p>
							</div>

							<!-- Multiple Flags -->
							<div>
								<div class="flex items-center justify-between mb-3">
									<label class="text-sm font-medium text-stone-300">Flags ({newChallenge.flags.length})</label>
									<button type="button" on:click={addFlag} class="text-sm text-stone-400 hover:text-white flex items-center gap-1 transition">
										<Icon icon="mdi:plus" class="w-4 h-4" />
										Add Flag
									</button>
								</div>
								<div class="space-y-3">
									{#each newChallenge.flags as flag, i}
										<div class="bg-black border border-stone-700 rounded-xl p-4">
											<div class="flex items-center gap-3 mb-3">
												<input
													type="text"
													bind:value={flag.name}
													class="flex-1 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm focus:outline-none focus:border-stone-500"
													placeholder="Flag name (e.g., User Flag)"
												/>
												<input
													type="number"
													bind:value={flag.points}
													min="1"
													class="w-24 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm text-center focus:outline-none focus:border-stone-500"
													placeholder="Points"
												/>
												{#if newChallenge.flags.length > 1}
													<button type="button" on:click={() => removeFlag(i)} class="text-red-400 hover:text-red-300 p-2 transition">
														<Icon icon="mdi:trash-can" class="w-5 h-5" />
													</button>
												{/if}
											</div>
											<input
												type="text"
												bind:value={flag.flag}
												required
												class="w-full px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white font-mono text-sm focus:outline-none focus:border-stone-500"
												placeholder="flag&#123;...&#125;"
											/>
										</div>
									{/each}
								</div>
								<p class="text-stone-500 text-xs mt-2">Total points: {newChallenge.flags.reduce((sum, f) => sum + (f.points || 0), 0)}</p>
							</div>
						</div>
					{/if}

					<!-- Actions -->
					<div class="flex gap-3 pt-4">
						<button
							type="submit"
							disabled={uploadLoading}
							class="flex-1 py-3 bg-white text-black text-sm font-bold rounded-xl hover:bg-stone-200 transition-all disabled:opacity-50 flex items-center justify-center gap-2"
						>
							{#if uploadLoading}
								<Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
								{uploadProgress > 0 ? `Uploading ${uploadProgress}%` : 'Creating...'}
							{:else}
								<Icon icon="mdi:plus" class="w-5 h-5" />
								Create Challenge
							{/if}
						</button>
						<button
							type="button"
							on:click={() => showCreateModal = false}
							disabled={uploadLoading}
							class="px-6 py-3 text-stone-400 text-sm font-medium hover:text-white transition disabled:opacity-50"
						>
							Cancel
						</button>
					</div>
				</form>
			</div>
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

<!-- Add Node Modal -->
{#if showNodeModal}
	<div 
		class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4"
		on:click={() => showNodeModal = false}
		on:keydown={(e) => e.key === 'Escape' && (showNodeModal = false)}
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
				<h2 class="text-lg font-medium text-white">Add VM Node</h2>
				<button on:click={() => showNodeModal = false} class="text-stone-400 hover:text-white transition">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={createNode} class="p-6 space-y-4">
				<div class="grid grid-cols-2 gap-4">
					<div class="col-span-2">
						<label class="block text-xs text-stone-500 mb-1.5">Node Name</label>
						<input
							type="text"
							bind:value={newNode.name}
							required
							placeholder="prod-node-01"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Hostname</label>
						<input
							type="text"
							bind:value={newNode.hostname}
							required
							placeholder="vm-node.example.com"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">IP Address</label>
						<input
							type="text"
							bind:value={newNode.ip_address}
							required
							placeholder="10.0.0.1"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
				</div>

				<div class="grid grid-cols-3 gap-4">
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Total vCPU</label>
						<input
							type="number"
							bind:value={newNode.total_vcpu}
							required
							min="1"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Memory (MB)</label>
						<input
							type="number"
							bind:value={newNode.total_memory_mb}
							required
							min="1024"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Disk (GB)</label>
						<input
							type="number"
							bind:value={newNode.total_disk_gb}
							required
							min="10"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Max VMs</label>
						<input
							type="number"
							bind:value={newNode.max_vms}
							required
							min="1"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Provider</label>
						<select
							bind:value={newNode.provider}
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						>
							<option value="gcp">Google Cloud</option>
							<option value="aws">AWS</option>
							<option value="azure">Azure</option>
							<option value="bare-metal">Bare Metal</option>
						</select>
					</div>
				</div>

				<div class="flex gap-3 pt-2">
					<button
						type="submit"
						disabled={actionLoading === 'create-node'}
						class="flex-1 py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition disabled:opacity-50"
					>
						{actionLoading === 'create-node' ? 'Creating...' : 'Add Node'}
					</button>
					<button
						type="button"
						on:click={() => showNodeModal = false}
						class="px-4 py-2.5 text-stone-400 text-sm hover:text-white transition"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}

<!-- Upload Template Modal -->
{#if showTemplateUploadModal}
	<div 
		class="fixed inset-0 bg-black/80 backdrop-blur-sm z-50 flex items-center justify-center p-4"
		on:click={() => showTemplateUploadModal = false}
		on:keydown={(e) => e.key === 'Escape' && (showTemplateUploadModal = false)}
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
				<h2 class="text-lg font-medium text-white">Upload VM Template</h2>
				<button on:click={() => showTemplateUploadModal = false} class="text-stone-400 hover:text-white transition">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={uploadTemplate} class="p-6 space-y-4">
				{#if templateUploading && templateUploadProgress > 0}
					<div class="py-3 px-4 bg-stone-900/50 rounded border border-stone-800">
						<div class="flex items-center justify-between mb-2">
							<span class="text-sm text-stone-300">Uploading...</span>
							<span class="text-sm text-stone-400">{templateUploadProgress}%</span>
						</div>
						<div class="w-full bg-stone-800 rounded-full h-2 overflow-hidden">
							<div class="bg-green-500 h-full transition-all" style="width: {templateUploadProgress}%"></div>
						</div>
					</div>
				{/if}

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Template Name</label>
					<input
						type="text"
						bind:value={templateName}
						required
						placeholder="moby-dock"
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
					/>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">Description</label>
					<textarea
						bind:value={templateDescription}
						rows="2"
						placeholder="Docker escape challenge..."
						class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700 resize-none"
					></textarea>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Min vCPU</label>
						<input
							type="number"
							bind:value={templateMinVcpu}
							min="1"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
					<div>
						<label class="block text-xs text-stone-500 mb-1.5">Min Memory (MB)</label>
						<input
							type="number"
							bind:value={templateMinMemory}
							min="512"
							class="w-full px-3 py-2 bg-black border border-stone-800 rounded text-white text-sm focus:outline-none focus:border-stone-700"
						/>
					</div>
				</div>

				<div>
					<label class="block text-xs text-stone-500 mb-1.5">OVA/QCOW2/VMDK File</label>
					<div class="border-2 border-dashed border-stone-700 rounded-lg p-6 text-center hover:border-stone-500 transition-colors">
						{#if templateFile}
							<div class="flex items-center justify-center gap-3">
								<Icon icon="mdi:file-check" class="w-6 h-6 text-green-400" />
								<div class="text-left">
									<p class="text-white text-sm">{templateFile.name}</p>
									<p class="text-stone-500 text-xs">{(templateFile.size / 1024 / 1024 / 1024).toFixed(2)} GB</p>
								</div>
								<button type="button" on:click={() => templateFile = null} class="text-red-400 hover:text-red-300 p-1">
									<Icon icon="mdi:close" class="w-4 h-4" />
								</button>
							</div>
						{:else}
							<Icon icon="mdi:cloud-upload" class="w-10 h-10 text-stone-600 mx-auto mb-2" />
							<p class="text-stone-400 text-sm mb-2">Drop file or click to browse</p>
							<input 
								type="file" 
								accept=".ova,.qcow2,.vmdk" 
								on:change={(e) => templateFile = e.target.files?.[0] || null} 
								class="hidden" 
								id="template-upload" 
							/>
							<label for="template-upload" class="inline-block px-3 py-1.5 bg-stone-800 text-white rounded text-xs cursor-pointer hover:bg-stone-700 transition">
								Select File
							</label>
						{/if}
					</div>
				</div>

				<div class="flex gap-3 pt-2">
					<button
						type="submit"
						disabled={templateUploading || !templateFile || !templateName}
						class="flex-1 py-2.5 bg-white text-black text-sm font-medium rounded hover:bg-stone-200 transition disabled:opacity-50"
					>
						{templateUploading ? 'Uploading...' : 'Upload Template'}
					</button>
					<button
						type="button"
						on:click={() => showTemplateUploadModal = false}
						disabled={templateUploading}
						class="px-4 py-2.5 text-stone-400 text-sm hover:text-white transition disabled:opacity-50"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}
