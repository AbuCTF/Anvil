<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$api';
	import Icon from '@iconify/svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { difficultyClass, resourceClass, resourceIcon, resourceLabel } from '$lib/rank';

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
	let activeDockerInstances: any[] = [];

	// Audit data
	let intelLoading = false;
	let flagShares: any[] = [];
	let instanceFlags: any[] = [];

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
	let templateMinVcpu = 1;
	let templateMinMemory = 1024;
	let templateUploadProgress = 0;
	let templateUploading = false;

	// Platform settings
	let platformSettings: Record<string, any> = {};
	let savingSettings = false;
	let settingsChanged = false;

	// Challenge creation
	let categories: any[] = [];
	let newChallenge = {
		name: '',
		description: '',
		category: '',
		category_id: '',
		newCategoryName: '',
		difficulty: 'easy',
		base_points: 100,
		flag: '',
		flags: [{ name: 'User Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }, { name: 'Root Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }],
		type: 'container',
		docker_image: '',
		container_platform: '',
		exposed_ports: [{ port: 1337, protocol: 'tcp', service: 'tcp' }],
		ova_url: '',
		vm_template_id: '',
		vm_source: 'template',
		files: [],
		// Timer settings
		instance_timeout: 120,
		max_extensions: 3,
		vm_timeout_minutes: 60,
		vm_max_extensions: 2,
		vm_extension_minutes: 30,
		cooldown_minutes: 15
	};
	let uploadLoading = false;
	let uploadError = '';
	let ovaFile: File | null = null;
	let uploadProgress = 0;

	// File attachments for challenge creation
	interface PendingAttachment {
		file: File;
		description: string;
	}
	let pendingAttachments: PendingAttachment[] = [];
	let attachmentUploadStatus = '';

	// Shared design-system class tokens (see DESIGN.md).
	const fieldCls = 'px-3 py-2 bg-stone-950 border border-stone-800 rounded-md text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-500 transition-colors';
	const labelCls = 'block text-xs font-medium text-stone-400 mb-1.5';
	const btnPrimary = 'inline-flex items-center justify-center gap-2 px-3.5 py-2 rounded-md bg-amber-500/90 text-stone-950 text-sm leading-none font-medium hover:bg-amber-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed';
	const btnGhost = 'inline-flex items-center justify-center gap-2 px-3.5 py-2 rounded-md border border-stone-700 text-stone-300 text-sm leading-none font-medium hover:bg-stone-800/40 hover:text-stone-200 transition-colors disabled:opacity-50 disabled:cursor-not-allowed';

	function handleKeydown(e: KeyboardEvent) {
		if (e.key !== 'Escape') return;
		if (showCreateModal) showCreateModal = false;
		else if (showEditModal) { showEditModal = false; editingChallenge = null; }
		else if (showNodeModal) showNodeModal = false;
		else if (showTemplateUploadModal) showTemplateUploadModal = false;
	}

	function addPendingAttachment(event: Event) {
		const input = event.target as HTMLInputElement;
		if (input.files) {
			for (const file of Array.from(input.files)) {
				pendingAttachments = [...pendingAttachments, { file, description: '' }];
			}
			// Reset the input so the same file can be re-added if needed
			input.value = '';
		}
	}

	function removePendingAttachment(index: number) {
		pendingAttachments = pendingAttachments.filter((_, i) => i !== index);
	}

	function formatFileSize(bytes: number): string {
		if (bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
		return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
	}

	function addFlag() {
		newChallenge.flags = [...newChallenge.flags, { name: `Flag ${newChallenge.flags.length + 1}`, flag: '', points: 25, flag_type: 'static', dynamic_flag_prefix: '' }];
	}

	function removeFlag(index: number) {
		newChallenge.flags = newChallenge.flags.filter((_, i) => i !== index);
	}

	function removeEditPort(index: number) {
		if (!editingChallenge?.exposed_ports) return;
		editingChallenge.exposed_ports = editingChallenge.exposed_ports.filter((_p: any, idx: number) => idx !== index);
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
		editingChallenge = {
			...challenge,
			// Ensure arrays/optional fields have proper defaults for the form
			exposed_ports: challenge.exposed_ports || [],
			instance_timeout: challenge.instance_timeout ?? 120,
			max_extensions: challenge.max_extensions ?? 3,
			cooldown_minutes: challenge.cooldown_minutes ?? 15,
			container_image: challenge.container_image || '',
			container_tag: challenge.container_tag || 'latest',
			container_platform: challenge.container_platform || '',
			cpu_limit: challenge.cpu_limit || '1',
			memory_limit: challenge.memory_limit || '512m',
			author_name: challenge.author_name || '',
			category_id: challenge.category_id || '',
		};
		showEditModal = true;
	}

	async function handleEditChallenge() {
		if (!editingChallenge) return;
		actionLoading = editingChallenge.id;
		try {
			const payload: any = {
				name: editingChallenge.name,
				description: editingChallenge.description,
				difficulty: editingChallenge.difficulty,
				base_points: editingChallenge.base_points,
				resource_type: editingChallenge.resource_type,
				author_name: editingChallenge.author_name || '',
				instance_timeout: editingChallenge.instance_timeout,
				max_extensions: editingChallenge.max_extensions,
				cooldown_minutes: editingChallenge.cooldown_minutes,
			};

			// Category: send category_id (may be empty string to clear it) or fallback to name
			if (categories.length > 0) {
				// categories dropdown was shown — send the selected ID (or null to clear)
				payload.category_id = editingChallenge.category_id || null;
			} else if (editingChallenge.category_name) {
				// free-text fallback — send by name for backend resolution
				payload.category = editingChallenge.category_name;
			}

			// Docker-specific fields
			if (editingChallenge.resource_type !== 'vm') {
				payload.container_image = editingChallenge.container_image;
				payload.container_tag = editingChallenge.container_tag || 'latest';
				payload.container_platform = editingChallenge.container_platform;
				payload.cpu_limit = editingChallenge.cpu_limit;
				payload.memory_limit = editingChallenge.memory_limit;
				if (Array.isArray(editingChallenge.exposed_ports)) {
					payload.exposed_ports = editingChallenge.exposed_ports.filter((p: any) => p.port > 0);
				}
			}

			// VM-specific fields
			if (editingChallenge.resource_type === 'vm' && editingChallenge.vm_template_id) {
				payload.vm_template_id = editingChallenge.vm_template_id;
			}

			await api.updateAdminChallenge(editingChallenge.id, payload);
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
		await loadDashboard();
	});

	async function loadDashboard() {
		loading = true;
		try {
			const [statsRes, usersRes, challengesRes, categoriesRes] = await Promise.all([
				api.getAdminStats(),
				api.getAdminUsers(),
				api.getAdminChallenges(),
				api.getAdminCategories().catch(() => ({ categories: [] }))
			]);
			stats = statsRes;
			users = usersRes.users || [];
			challenges = challengesRes.challenges || [];
			categories = categoriesRes.categories || [];
			error = '';

			// Load infrastructure data in parallel
			try {
				const [infraRes, nodesRes, templatesRes, instancesRes, dockerInstancesRes] = await Promise.all([
					api.getInfrastructureStats(),
					api.getNodes(),
					api.getVMTemplates(),
					api.getActiveInstances(),
					api.getActiveDockerInstances()
				]);
				infraStats = infraRes;
				nodes = nodesRes.nodes || [];
				templates = templatesRes.templates || [];
				activeInstances = instancesRes.instances || [];
				activeDockerInstances = dockerInstancesRes.instances || [];
			} catch {
				// Infrastructure endpoints may not be available yet
				infraStats = null;
				nodes = [];
				templates = [];
				activeInstances = [];
				activeDockerInstances = [];
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

	async function loadIntel() {
		intelLoading = true;
		try {
			const [sharesRes, flagsRes] = await Promise.all([
				api.getFlagShares(),
				api.getInstanceFlags()
			]);
			flagShares = sharesRes.flag_shares || [];
			instanceFlags = flagsRes.instance_flags || [];
		} catch {
			// non-critical
		} finally {
			intelLoading = false;
		}
	}

	function setTab(id: string) {
		activeTab = id;
		if (id === 'intel' && flagShares.length === 0 && instanceFlags.length === 0) {
			loadIntel();
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

	function handleNumberInput(e: Event, key: string) {
		const target = e.target as HTMLInputElement;
		updateSetting(key, parseInt(target.value) || 0);
	}

	function handleTextInput(e: Event, key: string) {
		const target = e.target as HTMLInputElement;
		updateSetting(key, target.value);
	}

	function handleSelectChange(e: Event, key: string) {
		const target = e.target as HTMLSelectElement;
		updateSetting(key, target.value === 'true');
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

	async function deleteInstance(instanceId: string) {
		if (!confirm('Force stop and remove this VM instance? The user will lose their session.')) return;
		actionLoading = instanceId;
		try {
			await api.forceStopAdminInstance(instanceId);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to stop instance');
		} finally {
			actionLoading = '';
		}
	}

	async function deleteDockerInstance(instanceId: string) {
		if (!confirm('Force stop and remove this Docker instance? The user will lose their session.')) return;
		actionLoading = instanceId;
		try {
			await api.forceStopAdminInstance(instanceId);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to stop Docker instance');
		} finally {
			actionLoading = '';
		}
	}

	async function deleteUser(userId: string) {
		if (!confirm('Delete this user? This action cannot be undone.')) return;
		actionLoading = userId;
		try {
			await api.deleteAdminUser(userId);
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to delete user');
		} finally {
			actionLoading = '';
		}
	}

	async function changeUserRole(userId: string, newRole: string) {
		if (!confirm(`Change user role to ${newRole}?`)) return;
		actionLoading = userId;
		try {
			await api.updateAdminUser(userId, { role: newRole });
			await loadDashboard();
		} catch (e) {
			alert(e instanceof Error ? e.message : 'Failed to change user role');
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
		attachmentUploadStatus = '';

		// Resolve the category: either an existing ID or a new category name
		let categoryId: string | undefined;
		let categoryName: string | undefined;

		if (newChallenge.category_id === '__new__') {
			// User wants to create a new category - send by name for backend resolution
			const trimmed = (newChallenge.newCategoryName || '').trim();
			if (!trimmed) {
				uploadError = 'Please enter a name for the new category.';
				uploadLoading = false;
				return;
			}
			categoryName = trimmed;
		} else if (newChallenge.category_id) {
			// Existing category selected by ID
			categoryId = newChallenge.category_id;
		} else if (newChallenge.category) {
			// Free-text fallback (no categories loaded)
			categoryName = newChallenge.category;
		}

		try {
			let createdChallengeId: string | undefined;

			if (newChallenge.type === 'ova') {
				if (newChallenge.vm_source === 'template' && newChallenge.vm_template_id) {
					// Create VM challenge using existing template
					const result = await api.createAdminChallenge({
						name: newChallenge.name,
						description: newChallenge.description,
						difficulty: newChallenge.difficulty,
						base_points: newChallenge.base_points,
						...(categoryId ? { category_id: categoryId } : {}),
						...(categoryName ? { category: categoryName } : {}),
						challenge_type: 'vm',
						vm_template_id: newChallenge.vm_template_id,
						vcpu: 1,
						memory_mb: 1024,
						flags: newChallenge.flags
					});
					createdChallengeId = result?.id;
				} else if (ovaFile) {
					// OVA upload using FormData
					const formData = new FormData();
					formData.append('file', ovaFile);
					formData.append('name', newChallenge.name);
					formData.append('description', newChallenge.description);
					formData.append('difficulty', newChallenge.difficulty);
					formData.append('base_points', String(newChallenge.base_points));
					if (categoryId) formData.append('category_id', categoryId);
					else if (categoryName) formData.append('category', categoryName);
					formData.append('flags', JSON.stringify(newChallenge.flags));

					const result = await api.uploadOvaChallenge(formData, (progress) => {
						uploadProgress = progress;
					});
					createdChallengeId = result?.id;
				} else {
					throw new Error('Please select a template or upload an OVA file');
				}
			} else {
				// Container challenge
				const result = await api.createAdminChallenge({
					name: newChallenge.name,
					description: newChallenge.description,
					difficulty: newChallenge.difficulty,
					base_points: newChallenge.base_points,
					...(categoryId ? { category_id: categoryId } : {}),
					...(categoryName ? { category: categoryName } : {}),
					challenge_type: 'docker',
					container_image: newChallenge.docker_image,
					container_platform: newChallenge.container_platform,
					exposed_ports: newChallenge.exposed_ports.filter(p => p.port > 0),
					flags: newChallenge.flags.map((f, i) => ({
						name: f.name,
						flag: f.flag,
						points: f.points,
						sort_order: i + 1,
						flag_type: f.flag_type || 'static',
						dynamic_flag_prefix: f.dynamic_flag_prefix || ''
					})),
					instance_timeout: newChallenge.instance_timeout,
					max_extensions: newChallenge.max_extensions,
					cooldown_minutes: newChallenge.cooldown_minutes
				});
				createdChallengeId = result?.id;
			}

			// Upload any pending file attachments
			const failedFiles: string[] = [];
			if (createdChallengeId && pendingAttachments.length > 0) {
				for (let i = 0; i < pendingAttachments.length; i++) {
					const { file, description } = pendingAttachments[i];
					attachmentUploadStatus = `Uploading file ${i + 1} of ${pendingAttachments.length}: ${file.name}`;
					const fd = new FormData();
					fd.append('file', file);
					if (description) fd.append('description', description);
					try {
						await api.uploadAttachment(createdChallengeId, fd);
					} catch (uploadErr) {
						console.warn(`Failed to upload attachment "${file.name}":`, uploadErr);
						failedFiles.push(file.name);
					}
				}
				attachmentUploadStatus = '';
			}

			// Challenge was created — close modal and reset form regardless of attachment failures
			showCreateModal = false;
			await loadDashboard();

			// Reset form
			newChallenge = {
				name: '',
				description: '',
				category: '',
				category_id: '',
				newCategoryName: '',
				difficulty: 'easy',
				base_points: 100,
				flag: '',
				flags: [{ name: 'User Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }, { name: 'Root Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }],
				type: 'container',
				docker_image: '',
				container_platform: '',
				exposed_ports: [{ port: 1337, protocol: 'tcp', service: 'tcp' }],
				ova_url: '',
				vm_template_id: '',
				vm_source: 'template',
				files: [],
				instance_timeout: 120,
				max_extensions: 3,
				vm_timeout_minutes: 60,
				vm_max_extensions: 2,
				vm_extension_minutes: 30,
				cooldown_minutes: 15
			};
			ovaFile = null;
			pendingAttachments = [];

			if (failedFiles.length > 0) {
				// Challenge was created — surface file upload failures as a page-level warning
				// so the admin can re-upload from the challenge detail page
				const maxShown = 3;
				const shown = failedFiles.slice(0, maxShown).join(', ');
				const extra = failedFiles.length > maxShown ? ` and ${failedFiles.length - maxShown} more` : '';
				error = `Challenge created, but ${failedFiles.length} file(s) failed to upload: ${shown}${extra}. You can re-upload them from the challenge detail page.`;
			}
		} catch (e) {
			uploadError = e instanceof Error ? e.message : 'Failed to create challenge';
		} finally {
			uploadLoading = false;
			attachmentUploadStatus = '';
		}
	}

	const TABS = [
		{ id: 'overview', label: 'Dashboard', icon: 'mdi:view-dashboard-outline' },
		{ id: 'challenges', label: 'Challenges', icon: 'mdi:flag-variant-outline' },
		{ id: 'users', label: 'Users', icon: 'mdi:account-group-outline' },
		{ id: 'infrastructure', label: 'System', icon: 'mdi:server-network' },
		{ id: 'settings', label: 'Settings', icon: 'mdi:cog-outline' },
		{ id: 'intel', label: 'Audit', icon: 'mdi:shield-search' }
	];
</script>

<svelte:head>
	<title>Admin - Anvil</title>
</svelte:head>

<svelte:window on:keydown={handleKeydown} />

<div class="min-h-screen bg-stone-950">
	<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		<PageHeader title="Admin" subtitle="Platform management">
			<div slot="actions">
				{#if activeTab === 'challenges'}
					<button on:click={() => showCreateModal = true} class={btnPrimary}>
						<Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />
						New Challenge
					</button>
				{:else if activeTab === 'infrastructure'}
					<div class="flex flex-wrap gap-2">
						<button on:click={() => showTemplateUploadModal = true} class={btnGhost}>
							<Icon icon="mdi:upload" class="w-3.5 h-3.5 shrink-0" />
							Upload Template
						</button>
						<button on:click={() => showNodeModal = true} class={btnPrimary}>
							<Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />
							Add Node
						</button>
					</div>
				{/if}
			</div>
		</PageHeader>

		{#if loading}
			<div class="flex items-center justify-center min-h-[40vh]">
				<Icon icon="mdi:loading" class="w-6 h-6 text-stone-600 animate-spin" />
			</div>
		{:else if error}
			<EmptyState icon="mdi:alert-circle-outline" text={error} />
		{:else}
			<!-- Tabs -->
			<div class="border-b border-stone-800 mb-8 overflow-x-auto">
				<div class="flex gap-1 min-w-max">
					{#each TABS as tab}
						<button
							type="button"
							on:click={() => setTab(tab.id)}
							class="relative px-3.5 py-2.5 text-sm leading-none font-medium whitespace-nowrap flex items-center gap-2 transition-colors {activeTab === tab.id ? 'text-stone-100' : 'text-stone-500 hover:text-stone-300'}"
						>
							<Icon icon={tab.icon} class="w-3.5 h-3.5 shrink-0" />
							{tab.label}
							{#if activeTab === tab.id}
								<span class="absolute inset-x-2 -bottom-px h-0.5 bg-stone-400"></span>
							{/if}
						</button>
					{/each}
				</div>
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
						<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
							<p class="text-xs text-stone-500 uppercase tracking-wide">{stat.label}</p>
							<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">{stat.value.toLocaleString()}</p>
						</div>
					{/each}
				</div>

				<!-- Recent Activity -->
				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
					<!-- Recent Users -->
					<Card title="Recent Users" bodyClass="">
						{#if users.length === 0}
							<EmptyState icon="mdi:account-off-outline" text="No users yet." />
						{:else}
							<div class="divide-y divide-stone-800/60">
								{#each users.slice(0, 5) as user}
									<div class="px-4 py-3 flex items-center justify-between gap-3">
										<div class="flex items-center gap-3 min-w-0">
											<div class="w-8 h-8 bg-stone-800 rounded-full flex items-center justify-center shrink-0">
												<span class="text-xs font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
											</div>
											<div class="min-w-0">
												<p class="text-sm text-stone-200 truncate">{user.username}</p>
												<p class="text-xs text-stone-500 truncate">{user.email}</p>
											</div>
										</div>
										<span class="text-xs text-stone-600 tabular-nums shrink-0">{formatDate(user.created_at)}</span>
									</div>
								{/each}
							</div>
						{/if}
					</Card>

					<!-- Top Challenges -->
					<Card title="Top Challenges" bodyClass="">
						{#if challenges.length === 0}
							<EmptyState icon="mdi:flag-outline" text="No challenges yet." />
						{:else}
							<div class="divide-y divide-stone-800/60">
								{#each challenges.slice(0, 5) as challenge}
									<div class="px-4 py-3 flex items-center justify-between gap-3">
										<div class="min-w-0">
											<p class="text-sm text-stone-200 truncate">{challenge.name}</p>
											<span class="inline-flex items-center px-2 py-0.5 mt-1 rounded-full border text-[0.7rem] capitalize {difficultyClass(challenge.difficulty)}">{challenge.difficulty}</span>
										</div>
										<span class="text-xs text-stone-400 tabular-nums shrink-0">{challenge.total_solves || 0} solves</span>
									</div>
								{/each}
							</div>
						{/if}
					</Card>
				</div>
			{/if}

			<!-- Challenges Tab -->
			{#if activeTab === 'challenges'}
				{#if challenges.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:flag-outline" text="No challenges yet.">
							<button on:click={() => showCreateModal = true} class="mt-3 text-sm text-amber-500/90 hover:text-amber-400 transition-colors">
								Create your first challenge →
							</button>
						</EmptyState>
					</Card>
				{:else}
					<!-- Mobile: Card view -->
					<div class="lg:hidden space-y-3">
						{#each challenges as challenge}
							<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
								<div class="flex items-start justify-between gap-3 mb-3">
									<div class="min-w-0">
										<a href="/challenges/{challenge.slug}" class="text-sm font-medium text-stone-200 hover:text-amber-400 transition-colors">{challenge.name}</a>
									<div class="flex items-center flex-wrap gap-1.5 mt-1.5 leading-none">
										<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}">{challenge.difficulty}</span>
										<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
											<Icon icon={resourceIcon(challenge.resource_type)} class="w-3 h-3 shrink-0" />{resourceLabel(challenge.resource_type)}
											</span>
										</div>
									</div>
								<span class="inline-flex items-center gap-1.5 text-xs leading-none shrink-0 {challenge.status === 'published' ? 'text-up' : 'text-warn'}">
										<span class="w-1.5 h-1.5 rounded-full {challenge.status === 'published' ? 'bg-up' : 'bg-warn'}"></span>
										{challenge.status === 'published' ? 'Published' : 'Draft'}
									</span>
								</div>
								<div class="flex items-center justify-between text-xs text-stone-500 mb-3 tabular-nums">
									<span>{challenge.base_points} pts</span>
									<span>{challenge.total_solves || 0} solves</span>
								</div>
								<div class="flex items-center gap-2 pt-3 border-t border-stone-800">
									<button
										on:click={() => openEditModal(challenge)}
									class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-stone-300 border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
										disabled={actionLoading === challenge.id}
										title="Edit challenge"
									>
									<Icon icon="mdi:pencil" class="w-3 h-3 shrink-0" />
										Edit
									</button>
									{#if challenge.status === 'draft'}
										<button
											on:click={() => publishChallenge(challenge)}
										class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-up border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
											disabled={actionLoading === challenge.id}
											title="Publish challenge"
										>
										<Icon icon="mdi:rocket-launch-outline" class="w-3 h-3 shrink-0" />
											Publish
										</button>
									{:else}
										<button
											on:click={() => unpublishChallenge(challenge)}
										class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-warn border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
											disabled={actionLoading === challenge.id}
											title="Unpublish challenge"
										>
										<Icon icon="mdi:eye-off-outline" class="w-3 h-3 shrink-0" />
											Unpublish
										</button>
									{/if}
									<button
										on:click={() => deleteChallenge(challenge)}
									class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs leading-none font-medium text-down border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50 ml-auto"
										disabled={actionLoading === challenge.id}
										title="Delete challenge"
									>
									<Icon icon="mdi:trash-can-outline" class="w-3 h-3 shrink-0" />
									</button>
								</div>
							</div>
						{/each}
					</div>

					<!-- Desktop: Table view -->
					<div class="hidden lg:block">
						<Card title="Challenges" bodyClass="">
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{challenges.length}</span>
							<div class="overflow-x-auto">
								<table class="w-full min-w-[720px] text-sm">
									<thead>
										<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
											<th class="px-4 py-2.5 text-left font-medium">Name</th>
											<th class="px-4 py-2.5 text-left font-medium">Difficulty</th>
											<th class="px-4 py-2.5 text-left font-medium">Type</th>
											<th class="px-4 py-2.5 text-right font-medium">Points</th>
											<th class="px-4 py-2.5 text-right font-medium">Solves</th>
											<th class="px-4 py-2.5 text-left font-medium">Status</th>
											<th class="px-4 py-2.5 text-right font-medium">Actions</th>
										</tr>
									</thead>
									<tbody>
										{#each challenges as challenge}
											<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
												<td class="px-4 py-2.5">
													<a href="/challenges/{challenge.slug}" class="text-stone-200 hover:text-amber-400 transition-colors">{challenge.name}</a>
												</td>
												<td class="px-4 py-2.5">
												<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}">{challenge.difficulty}</span>
												</td>
												<td class="px-4 py-2.5">
												<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
													<Icon icon={resourceIcon(challenge.resource_type)} class="w-3 h-3 shrink-0" />{resourceLabel(challenge.resource_type)}
													</span>
												</td>
												<td class="px-4 py-2.5 text-right text-stone-200 tabular-nums">{challenge.base_points}</td>
												<td class="px-4 py-2.5 text-right text-stone-400 tabular-nums">{challenge.total_solves || 0}</td>
												<td class="px-4 py-2.5">
												<span class="inline-flex items-center gap-1.5 text-xs leading-none {challenge.status === 'published' ? 'text-up' : 'text-warn'}">
														<span class="w-1.5 h-1.5 rounded-full {challenge.status === 'published' ? 'bg-up' : 'bg-warn'}"></span>
														{challenge.status === 'published' ? 'Published' : 'Draft'}
													</span>
												</td>
												<td class="px-4 py-2.5">
													<div class="flex items-center justify-end gap-1">
														<button
															on:click={() => openEditModal(challenge)}
															class="p-2 text-stone-400 hover:text-stone-100 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
															disabled={actionLoading === challenge.id}
															title="Edit"
														>
															<Icon icon="mdi:pencil" class="w-4 h-4" />
														</button>
														{#if challenge.status === 'draft'}
															<button
																on:click={() => publishChallenge(challenge)}
																class="p-2 text-up hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
																disabled={actionLoading === challenge.id}
																title="Publish"
															>
																<Icon icon="mdi:rocket-launch-outline" class="w-4 h-4" />
															</button>
														{:else}
															<button
																on:click={() => unpublishChallenge(challenge)}
																class="p-2 text-warn hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
																disabled={actionLoading === challenge.id}
																title="Unpublish"
															>
																<Icon icon="mdi:eye-off-outline" class="w-4 h-4" />
															</button>
														{/if}
														<button
															on:click={() => deleteChallenge(challenge)}
															class="p-2 text-down hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
															disabled={actionLoading === challenge.id}
															title="Delete"
														>
															<Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
														</button>
													</div>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</Card>
					</div>
				{/if}
			{/if}

			<!-- Users Tab -->
			{#if activeTab === 'users'}
				{#if users.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:account-off-outline" text="No users yet." />
					</Card>
				{:else}
					<!-- Mobile: Card view -->
					<div class="lg:hidden space-y-3">
						{#each users as user}
							<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
								<div class="flex items-center gap-3 mb-3">
									<div class="w-10 h-10 bg-stone-800 rounded-full flex items-center justify-center shrink-0">
										<span class="text-sm font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
									</div>
									<div class="flex-1 min-w-0">
										<p class="text-sm font-medium text-stone-200 truncate">{user.username}</p>
										<p class="text-xs text-stone-500 truncate">{user.email}</p>
									</div>
									<span class="text-xs {user.role === 'admin' ? 'text-amber-500/90' : 'text-stone-400'}">{user.role}</span>
								</div>
								<div class="flex items-center justify-between text-xs text-stone-500 pt-3 border-t border-stone-800 tabular-nums">
									<span>{user.total_score || 0} points</span>
									<span>Joined {formatDate(user.created_at)}</span>
								</div>
								<div class="flex items-center justify-between gap-2 mt-3 pt-3 border-t border-stone-800">
									<select
										value={user.role}
										on:change={(e) => changeUserRole(user.id, e.currentTarget.value)}
										disabled={actionLoading === user.id}
										class="text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-1 text-stone-200 focus:outline-none focus:border-stone-500"
									>
										<option value="user">User</option>
										<option value="admin">Admin</option>
									</select>
									<button
										on:click={() => deleteUser(user.id)}
										disabled={actionLoading === user.id}
										class="text-xs text-down hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
										title="Delete user"
									>
										{actionLoading === user.id ? 'Deleting...' : 'Delete'}
									</button>
								</div>
							</div>
						{/each}
					</div>

					<!-- Desktop: Table view -->
					<div class="hidden lg:block">
						<Card title="Users" bodyClass="">
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{users.length}</span>
							<div class="overflow-x-auto">
								<table class="w-full min-w-[680px] text-sm">
									<thead>
										<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
											<th class="px-4 py-2.5 text-left font-medium">User</th>
											<th class="px-4 py-2.5 text-left font-medium hidden md:table-cell">Email</th>
											<th class="px-4 py-2.5 text-left font-medium">Role</th>
											<th class="px-4 py-2.5 text-right font-medium">Score</th>
											<th class="px-4 py-2.5 text-right font-medium hidden md:table-cell">Joined</th>
											<th class="px-4 py-2.5 text-right font-medium">Actions</th>
										</tr>
									</thead>
									<tbody>
										{#each users as user}
											<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
												<td class="px-4 py-2.5">
													<div class="flex items-center gap-3">
														<div class="w-8 h-8 bg-stone-800 rounded-full flex items-center justify-center shrink-0">
															<span class="text-xs font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
														</div>
														<span class="text-stone-200">{user.username}</span>
													</div>
												</td>
												<td class="px-4 py-2.5 text-stone-400 hidden md:table-cell">{user.email}</td>
												<td class="px-4 py-2.5">
													<span class="text-xs {user.role === 'admin' ? 'text-amber-500/90' : 'text-stone-400'}">{user.role}</span>
												</td>
												<td class="px-4 py-2.5 text-right text-stone-200 tabular-nums">{user.total_score || 0}</td>
												<td class="px-4 py-2.5 text-right text-stone-500 tabular-nums hidden md:table-cell">{formatDate(user.created_at)}</td>
												<td class="px-4 py-2.5">
													<div class="flex items-center justify-end gap-3">
														<select
															value={user.role}
															on:change={(e) => changeUserRole(user.id, e.currentTarget.value)}
															disabled={actionLoading === user.id}
															class="text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-1 text-stone-200 focus:outline-none focus:border-stone-500"
														>
															<option value="user">User</option>
															<option value="admin">Admin</option>
														</select>
														<button
															on:click={() => deleteUser(user.id)}
															disabled={actionLoading === user.id}
															class="text-xs text-down hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
															title="Delete user"
														>
															{actionLoading === user.id ? 'Deleting...' : 'Delete'}
														</button>
													</div>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</Card>
					</div>
				{/if}
			{/if}

			<!-- Infrastructure Tab -->
			{#if activeTab === 'infrastructure'}
				<!-- Stats Cards -->
				<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wide">Nodes</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.nodes?.online || 0}/{infraStats?.nodes?.total || 0}
						</p>
						<p class="text-xs text-up mt-1">online</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wide">vCPU</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.vcpu?.used || 0}/{infraStats?.resources?.vcpu?.total || 0}
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.vcpu?.available || 0} available</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wide">Memory</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.memory_gb?.used || 0}/{infraStats?.resources?.memory_gb?.total || 0} GB
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.memory_gb?.available || 0} GB free</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="text-xs text-stone-500 uppercase tracking-wide">Running VMs</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">{infraStats?.vms?.running || 0}</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">of {infraStats?.vms?.total || 0} total</p>
					</div>
				</div>

				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
					<!-- Nodes Section -->
					<Card title="VM Nodes" bodyClass="">
						<span slot="meta" class="text-stone-500 text-xs tabular-nums">{nodes.length} nodes</span>
						{#if nodes.length === 0}
							<EmptyState icon="mdi:server-off" text="No nodes configured.">
								<button on:click={() => showNodeModal = true} class="mt-3 text-sm text-amber-500/90 hover:text-amber-400 transition-colors">
									Add your first node →
								</button>
							</EmptyState>
						{:else}
							<div class="divide-y divide-stone-800/60">
								{#each nodes as node}
									<div class="px-4 py-3">
										<div class="flex items-center justify-between gap-2 mb-2">
											<div class="flex items-center gap-2 min-w-0 leading-none">
												<div class="w-2 h-2 rounded-full shrink-0 {node.status === 'online' ? 'bg-up' : 'bg-down'}"></div>
												<span class="text-sm text-stone-200 font-medium truncate">{node.name}</span>
												{#if node.is_primary}
													<span class="text-[0.7rem] text-amber-500/90 border border-amber-500/30 px-1.5 py-0.5 rounded-full shrink-0">primary</span>
												{/if}
											</div>
											<button
												on:click={() => deleteNode(node.id)}
												disabled={actionLoading === node.id}
												class="text-stone-500 hover:text-down transition-colors disabled:opacity-50 shrink-0"
												title="Delete node"
											>
												<Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
											</button>
										</div>
										<div class="text-xs text-stone-500 tabular-nums">
											<span>{node.ip_address}</span>
											<span class="mx-2 text-stone-700">•</span>
											<span>{node.active_vms}/{node.max_vms} VMs</span>
											<span class="mx-2 text-stone-700">•</span>
											<span>{node.used_vcpu}/{node.total_vcpu} vCPU</span>
										</div>
										<!-- Resource bars -->
										<div class="mt-2 space-y-1">
											<div class="flex items-center gap-2">
												<span class="text-xs text-stone-600 w-10">CPU</span>
												<div class="flex-1 h-1.5 bg-stone-800 rounded-full overflow-hidden">
													<div
														class="h-full bg-stone-400 transition-all"
														style="width: {node.total_vcpu ? (node.used_vcpu / node.total_vcpu * 100) : 0}%"
													></div>
												</div>
											</div>
											<div class="flex items-center gap-2">
												<span class="text-xs text-stone-600 w-10">RAM</span>
												<div class="flex-1 h-1.5 bg-stone-800 rounded-full overflow-hidden">
													<div
														class="h-full bg-stone-400 transition-all"
														style="width: {node.total_memory_mb ? (node.used_memory_mb / node.total_memory_mb * 100) : 0}%"
													></div>
												</div>
											</div>
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</Card>

					<!-- VM Templates Section -->
					<Card title="VM Templates" bodyClass="">
						<span slot="meta" class="text-stone-500 text-xs tabular-nums">{templates.length} templates</span>
						{#if templates.length === 0}
							<EmptyState icon="mdi:harddisk" text="No VM templates.">
								<button on:click={() => showTemplateUploadModal = true} class="mt-3 text-sm text-amber-500/90 hover:text-amber-400 transition-colors">
									Upload your first template →
								</button>
							</EmptyState>
						{:else}
							<div class="divide-y divide-stone-800/60">
								{#each templates as template}
									<div class="px-4 py-3">
										<div class="flex items-center justify-between gap-2 mb-1">
											<span class="text-sm text-stone-200 font-medium truncate">{template.name}</span>
											<div class="flex items-center gap-2 shrink-0">
												{#if template.is_active}
													<span class="text-xs text-up">Active</span>
												{:else}
													<span class="text-xs text-stone-500">Inactive</span>
												{/if}
												<button
													on:click={() => deleteTemplate(template.id)}
													disabled={actionLoading === template.id}
													class="text-stone-500 hover:text-down transition-colors disabled:opacity-50"
													title="Delete template"
												>
													<Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
												</button>
											</div>
										</div>
										<div class="text-xs text-stone-500 tabular-nums">
											<span>{((template.image_size_bytes || 0) / 1024 / 1024 / 1024).toFixed(1)} GB</span>
											<span class="mx-2 text-stone-700">•</span>
											<span>{template.vcpu || 1} vCPU / {template.memory_mb || 1024} MB</span>
										</div>
										{#if template.description}
											<p class="text-xs text-stone-600 mt-1 truncate">{template.description}</p>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					</Card>
				</div>

				<!-- Active Instances -->
				<div class="mb-8">
					<Card title="Active VM Instances" bodyClass="">
						<span slot="meta" class="text-stone-500 text-xs tabular-nums">{activeInstances.length} running</span>
						{#if activeInstances.length === 0}
							<EmptyState icon="mdi:desktop-mac-dashboard" text="No active VM instances." />
						{:else}
							<div class="overflow-x-auto">
								<table class="w-full min-w-[640px] text-sm">
									<thead>
										<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
											<th class="px-4 py-2.5 text-left font-medium">User</th>
											<th class="px-4 py-2.5 text-left font-medium">Challenge</th>
											<th class="px-4 py-2.5 text-left font-medium">IP</th>
											<th class="px-4 py-2.5 text-left font-medium">Status</th>
											<th class="px-4 py-2.5 text-right font-medium">Expires</th>
											<th class="px-4 py-2.5 text-right font-medium">Actions</th>
										</tr>
									</thead>
									<tbody>
										{#each activeInstances as instance}
											<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
												<td class="px-4 py-2.5 text-stone-200">{instance.username}</td>
												<td class="px-4 py-2.5 text-stone-300">{instance.challenge_name}</td>
												<td class="px-4 py-2.5 text-stone-400 font-mono text-xs">{instance.ip_address || '—'}</td>
												<td class="px-4 py-2.5">
													<span class="text-xs {instance.status === 'running' ? 'text-up' : 'text-warn'}">{instance.status}</span>
												</td>
												<td class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums">
													{instance.expires_at ? new Date(instance.expires_at * 1000).toLocaleTimeString() : '—'}
												</td>
												<td class="px-4 py-2.5 text-right">
													<button
														on:click={() => deleteInstance(instance.id)}
														disabled={actionLoading === instance.id}
														class="text-xs text-down hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
														title="Force stop and remove this instance"
													>
														{actionLoading === instance.id ? 'Stopping...' : 'Terminate'}
													</button>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/if}
					</Card>
				</div>

				<!-- Active Docker Instances -->
				<Card title="Active Docker Instances" bodyClass="">
					<span slot="meta" class="text-stone-500 text-xs tabular-nums">{activeDockerInstances.length} running</span>
					{#if activeDockerInstances.length === 0}
						<EmptyState icon="mdi:docker" text="No active Docker instances." />
					{:else}
						<div class="overflow-x-auto">
							<table class="w-full min-w-[640px] text-sm">
								<thead>
									<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
										<th class="px-4 py-2.5 text-left font-medium">User</th>
										<th class="px-4 py-2.5 text-left font-medium">Challenge</th>
										<th class="px-4 py-2.5 text-left font-medium">IP</th>
										<th class="px-4 py-2.5 text-left font-medium">Status</th>
										<th class="px-4 py-2.5 text-right font-medium">Expires</th>
										<th class="px-4 py-2.5 text-right font-medium">Actions</th>
									</tr>
								</thead>
								<tbody>
									{#each activeDockerInstances as instance}
										<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
											<td class="px-4 py-2.5 text-stone-200">{instance.username}</td>
											<td class="px-4 py-2.5 text-stone-300">{instance.challenge_name}</td>
											<td class="px-4 py-2.5 text-stone-400 font-mono text-xs">{instance.ip_address || '—'}</td>
											<td class="px-4 py-2.5">
												<span class="text-xs {instance.status === 'running' ? 'text-up' : 'text-warn'}">{instance.status}</span>
											</td>
											<td class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums">
												{instance.expires_at ? new Date(instance.expires_at * 1000).toLocaleTimeString() : '—'}
											</td>
											<td class="px-4 py-2.5 text-right">
												<button
													on:click={() => deleteDockerInstance(instance.id)}
													disabled={actionLoading === instance.id}
													class="text-xs text-down hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
													title="Force stop and remove this Docker instance"
												>
													{actionLoading === instance.id ? 'Stopping...' : 'Terminate'}
												</button>
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					{/if}
				</Card>
			{/if}

			<!-- Settings Tab -->
			{#if activeTab === 'settings'}
				<div class="space-y-6">
					<!-- Save Button -->
					{#if settingsChanged}
						<div class="flex justify-end">
							<button on:click={savePlatformSettings} disabled={savingSettings} class={btnPrimary}>
								{#if savingSettings}
									<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
								{:else}
									<Icon icon="mdi:content-save-outline" class="w-3.5 h-3.5 shrink-0" />
								{/if}
								Save Settings
							</button>
						</div>
					{/if}

					<!-- Instance Timeouts -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:timer-outline" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								Instance Timeouts
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Default session durations for VM instances by difficulty</p>
						</div>
						<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
							{#each [
								{ key: 'vm_timeout_easy', label: 'Easy', default: 90 },
								{ key: 'vm_timeout_medium', label: 'Medium', default: 120 },
								{ key: 'vm_timeout_hard', label: 'Hard', default: 180 },
								{ key: 'vm_timeout_insane', label: 'Insane', default: 240 }
							] as setting}
								<label class="block">
									<span class={labelCls}>{setting.label}</span>
									<div class="flex items-center gap-2">
										<input
											type="number"
											value={platformSettings[setting.key] ?? setting.default}
											on:input={(e) => handleNumberInput(e, setting.key)}
											min="30"
											max="480"
											class="w-full {fieldCls} tabular-nums"
										/>
										<span class="text-xs text-stone-500">min</span>
									</div>
								</label>
							{/each}
						</div>
					</Card>

					<!-- Cooldown Settings -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:timer-sand" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								Cooldown Periods
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Wait time before users can restart an instance after stopping</p>
						</div>
						<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
							{#each [
								{ key: 'cooldown_easy', label: 'Easy', default: 5 },
								{ key: 'cooldown_medium', label: 'Medium', default: 10 },
								{ key: 'cooldown_hard', label: 'Hard', default: 15 },
								{ key: 'cooldown_insane', label: 'Insane', default: 20 }
							] as setting}
								<label class="block">
									<span class={labelCls}>{setting.label}</span>
									<div class="flex items-center gap-2">
										<input
											type="number"
											value={platformSettings[setting.key] ?? setting.default}
											on:input={(e) => handleNumberInput(e, setting.key)}
											min="0"
											max="120"
											class="w-full {fieldCls} tabular-nums"
										/>
										<span class="text-xs text-stone-500">min</span>
									</div>
								</label>
							{/each}
						</div>
					</Card>

					<!-- Extension Settings -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:clock-plus-outline" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								Extension Settings
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">How many times and by how much users can extend their session</p>
						</div>
						<div class="grid grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>Max Extensions</span>
								<input
									type="number"
									value={platformSettings.max_extensions ?? 3}
									on:input={(e) => handleNumberInput(e, 'max_extensions')}
									min="0"
									max="10"
									class="w-full {fieldCls} tabular-nums"
								/>
							</label>
							<label class="block">
								<span class={labelCls}>Extension Duration</span>
								<div class="flex items-center gap-2">
									<input
										type="number"
										value={platformSettings.extension_minutes ?? 30}
										on:input={(e) => handleNumberInput(e, 'extension_minutes')}
										min="15"
										max="120"
										class="w-full {fieldCls} tabular-nums"
									/>
									<span class="text-xs text-stone-500">min</span>
								</div>
							</label>
						</div>
					</Card>

					<!-- User Limits -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:account-multiple-outline" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								User Limits
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Resource limits per user</p>
						</div>
						<div class="grid grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>Max Concurrent Instances</span>
								<input
									type="number"
									value={platformSettings.max_instances_per_user ?? 1}
									on:input={(e) => handleNumberInput(e, 'max_instances_per_user')}
									min="1"
									max="5"
									class="w-full {fieldCls} tabular-nums"
								/>
							</label>
							<label class="block">
								<span class={labelCls}>Max Daily Submissions</span>
								<input
									type="number"
									value={platformSettings.max_daily_submissions ?? 100}
									on:input={(e) => handleNumberInput(e, 'max_daily_submissions')}
									min="10"
									max="1000"
									class="w-full {fieldCls} tabular-nums"
								/>
							</label>
						</div>
					</Card>

					<!-- VPN Settings -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:vpn" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								VPN Settings
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">WireGuard VPN configuration</p>
						</div>
						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>VPN Server Endpoint</span>
								<input
									type="text"
									value={platformSettings.vpn_endpoint ?? 'play.abu.rocks:51820'}
									on:input={(e) => handleTextInput(e, 'vpn_endpoint')}
									class="w-full {fieldCls}"
								/>
							</label>
							<label class="block">
								<span class={labelCls}>Require VPN for Instances</span>
								<select
									value={platformSettings.require_vpn ?? 'true'}
									on:change={(e) => handleSelectChange(e, 'require_vpn')}
									class="w-full {fieldCls}"
								>
									<option value="true">Yes</option>
									<option value="false">No</option>
								</select>
							</label>
						</div>
					</Card>

					<!-- Platform Settings -->
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<Icon icon="mdi:cog-outline" class="w-3.5 h-3.5 shrink-0 text-stone-500" />
								Platform Settings
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">General platform configuration</p>
						</div>
						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>Allow Registration</span>
								<select
									value={platformSettings.registration_enabled ?? 'true'}
									on:change={(e) => handleSelectChange(e, 'registration_enabled')}
									class="w-full {fieldCls}"
								>
									<option value="true">Open</option>
									<option value="false">Closed</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Scoreboard</span>
								<select
									value={platformSettings.scoreboard_enabled ?? 'true'}
									on:change={(e) => handleSelectChange(e, 'scoreboard_enabled')}
									class="w-full {fieldCls}"
								>
									<option value="true">Public</option>
									<option value="false">Hidden</option>
								</select>
							</label>
						</div>
					</Card>
				</div>
			{:else if activeTab === 'intel'}
				<div class="space-y-8">
					<!-- Flag Share Events -->
					<div>
						<div class="flex items-center justify-between mb-4">
							<h3 class="text-sm font-semibold text-stone-200">Flag Share Events</h3>
							<button type="button" on:click={loadIntel} class="text-xs leading-none text-stone-500 hover:text-stone-300 flex items-center gap-1 transition-colors">
								<Icon icon="mdi:refresh" class="w-3 h-3 shrink-0" />
								Refresh
							</button>
						</div>
						{#if intelLoading}
							<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-8 flex justify-center">
								<Icon icon="mdi:loading" class="w-5 h-5 text-stone-600 animate-spin" />
							</div>
						{:else if flagShares.length === 0}
							<Card hasHeader={false}>
								<EmptyState icon="mdi:shield-check-outline" text="No flag share events detected." />
							</Card>
						{:else}
							<Card hasHeader={false} bodyClass="">
								<div class="overflow-x-auto">
									<table class="w-full min-w-[760px] text-sm">
										<thead>
											<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
												<th class="px-4 py-2.5 text-left font-medium">Challenge</th>
												<th class="px-4 py-2.5 text-left font-medium">Flag Owner</th>
												<th class="px-4 py-2.5 text-left font-medium">Submitter</th>
												<th class="px-4 py-2.5 text-left font-medium">IP</th>
												<th class="px-4 py-2.5 text-left font-medium">Flag Value</th>
												<th class="px-4 py-2.5 text-right font-medium">Time</th>
											</tr>
										</thead>
										<tbody>
											{#each flagShares as ev}
												<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
													<td class="px-4 py-2.5 text-stone-200">{ev.challenge_name ?? ev.challenge_id}</td>
													<td class="px-4 py-2.5 text-amber-500/90">{ev.owner_username ?? ev.owner_user_id}</td>
													<td class="px-4 py-2.5 text-down">{ev.submitter_username ?? ev.submitter_user_id}</td>
													<td class="px-4 py-2.5 text-xs text-stone-400 font-mono">{ev.submitter_ip ?? '—'}</td>
													<td class="px-4 py-2.5 text-xs text-stone-300 font-mono max-w-xs truncate">{ev.flag_value}</td>
													<td class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums">{new Date(ev.created_at).toLocaleString()}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							</Card>
						{/if}
					</div>

					<!-- Instance Flags -->
					<div>
						<h3 class="text-sm font-semibold text-stone-200 mb-4">Instance Flags ({instanceFlags.length})</h3>
						{#if intelLoading}
							<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-8 flex justify-center">
								<Icon icon="mdi:loading" class="w-5 h-5 text-stone-600 animate-spin" />
							</div>
						{:else if instanceFlags.length === 0}
							<Card hasHeader={false}>
								<EmptyState icon="mdi:flag-outline" text="No instance flags generated yet." />
							</Card>
						{:else}
							<Card hasHeader={false} bodyClass="">
								<div class="overflow-x-auto">
									<table class="w-full min-w-[640px] text-sm">
										<thead>
											<tr class="text-stone-500 text-[0.7rem] uppercase tracking-wider border-b border-stone-800">
												<th class="px-4 py-2.5 text-left font-medium">User</th>
												<th class="px-4 py-2.5 text-left font-medium">Challenge</th>
												<th class="px-4 py-2.5 text-left font-medium">Flag Value</th>
												<th class="px-4 py-2.5 text-left font-medium">Instance ID</th>
											</tr>
										</thead>
										<tbody>
											{#each instanceFlags as fl}
												<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
													<td class="px-4 py-2.5 text-stone-200">{fl.username ?? fl.user_id}</td>
													<td class="px-4 py-2.5 text-stone-300">{fl.challenge_name ?? fl.challenge_id}</td>
													<td class="px-4 py-2.5 text-xs text-up font-mono max-w-xs truncate">{fl.flag_value}</td>
													<td class="px-4 py-2.5 text-xs text-stone-500 font-mono">{fl.instance_id}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							</Card>
						{/if}
					</div>
				</div>
			{/if}
		{/if}
	</div>
</div>

<!-- Create Challenge Modal -->
{#if showCreateModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showCreateModal = false}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-2xl max-h-[90vh] flex flex-col" role="dialog" aria-modal="true">
			<!-- Header with Type Tabs -->
			<div class="p-6 border-b border-stone-800 flex-shrink-0">
				<div class="flex items-center justify-between mb-4">
					<h2 class="text-lg font-semibold text-stone-100 flex items-center gap-2">
						<Icon icon="mdi:plus-circle-outline" class="w-5 h-5 text-stone-400" />
						Create Challenge
					</h2>
					<button on:click={() => showCreateModal = false} class="text-stone-500 hover:text-stone-200 transition-colors p-1">
						<Icon icon="mdi:close" class="w-5 h-5" />
					</button>
				</div>

				<!-- Type Tabs at Top -->
				<div class="flex gap-1 p-1 bg-stone-950 border border-stone-800 rounded-md">
					<button
						type="button"
						on:click={() => newChallenge.type = 'container'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'container' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:docker" class="w-3.5 h-3.5 shrink-0" />
						Docker Container
					</button>
					<button
						type="button"
						on:click={() => newChallenge.type = 'ova'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'ova' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:desktop-classic" class="w-3.5 h-3.5 shrink-0" />
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
							<span class="text-sm text-stone-400 tabular-nums">{uploadProgress}%</span>
						</div>
						<div class="w-full bg-stone-800 rounded-full h-2 overflow-hidden">
							<div class="bg-amber-500/70 h-full transition-all duration-300" style="width: {uploadProgress}%"></div>
						</div>
					</div>
				{/if}

				<form on:submit|preventDefault={handleCreateChallenge} class="p-6 space-y-5">
					{#if uploadError}
						<div class="flex items-start gap-2 py-3 px-4 bg-down/10 border border-down/30 rounded-md text-down text-sm">
							<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
							{uploadError}
						</div>
					{/if}

					<!-- Basic Info -->
					<div class="grid grid-cols-1 md:grid-cols-2 gap-5">
						<label class="block md:col-span-2">
							<span class={labelCls}>Challenge Name *</span>
							<input
								type="text"
								bind:value={newChallenge.name}
								required
								class="w-full {fieldCls}"
								placeholder="Enter challenge name"
							/>
						</label>

						<label class="block">
							<span class={labelCls}>Category *</span>
							{#if categories.length > 0}
								<select
									bind:value={newChallenge.category_id}
									required={newChallenge.category_id !== '__new__'}
									class="w-full {fieldCls}"
								>
									<option value="" disabled>Select a category...</option>
									{#each categories as cat}
										<option value={cat.id}>{cat.name}</option>
									{/each}
									<option value="__new__">+ Add new category...</option>
								</select>
								{#if newChallenge.category_id === '__new__'}
									<input
										type="text"
										bind:value={newChallenge.newCategoryName}
										required
										class="w-full mt-2 {fieldCls}"
										placeholder="New category name"
									/>
								{/if}
							{:else}
								<input
									type="text"
									bind:value={newChallenge.category}
									required
									class="w-full {fieldCls}"
									placeholder="Web, Crypto, Pwn..."
								/>
							{/if}
						</label>

						<label class="block">
							<span class={labelCls}>Difficulty *</span>
							<select bind:value={newChallenge.difficulty} class="w-full {fieldCls}">
								<option value="easy">Easy</option>
								<option value="medium">Medium</option>
								<option value="hard">Hard</option>
								<option value="insane">Insane</option>
							</select>
						</label>

						<label class="block">
							<span class={labelCls}>Base Points *</span>
							<input
								type="number"
								bind:value={newChallenge.base_points}
								required
								min="1"
								class="w-full {fieldCls} tabular-nums"
							/>
						</label>

						<label class="block md:col-span-2">
							<span class={labelCls}>Description *</span>
							<textarea
								bind:value={newChallenge.description}
								required
								rows="3"
								class="w-full {fieldCls} resize-none"
								placeholder="Challenge description..."
							></textarea>
						</label>
					</div>

					<!-- Type-specific fields -->
					{#if newChallenge.type === 'container'}
						<div class="pt-4 border-t border-stone-800 space-y-5">
							<label class="block">
								<span class={labelCls}>Docker Image *</span>
								<input
									type="text"
									bind:value={newChallenge.docker_image}
									required
									class="w-full font-mono {fieldCls}"
									placeholder="ghcr.io/abuctf/token-overflow:latest"
								/>
								<p class="text-stone-500 text-xs mt-2">Pre-built image from a registry (GHCR, Docker Hub, etc.)</p>
							</label>

							<!-- Platform -->
							<label class="block">
								<span class={labelCls}>Platform</span>
								<select bind:value={newChallenge.container_platform} class="w-full {fieldCls}">
									<option value="">Auto (native architecture)</option>
									<option value="linux/amd64">linux/amd64</option>
									<option value="linux/arm64">linux/arm64</option>
								</select>
								<p class="text-stone-500 text-xs mt-2">Set if the image architecture differs from the server (e.g. amd64 image on ARM host)</p>
							</label>

							<!-- Exposed Ports -->
							<div>
								<div class="flex items-center justify-between mb-2">
									<span class="block text-xs font-medium text-stone-400">Exposed Ports</span>
									<button
										type="button"
										on:click={() => newChallenge.exposed_ports = [...newChallenge.exposed_ports, { port: 0, protocol: 'tcp', service: 'tcp' }]}
										class="text-xs leading-none text-stone-400 hover:text-stone-200 transition-colors flex items-center gap-1"
									>
										<Icon icon="mdi:plus" class="w-3 h-3 shrink-0" /> Add Port
									</button>
								</div>
								<div class="space-y-2">
									{#each newChallenge.exposed_ports as ep, i}
										<div class="flex items-center gap-2">
											<input
												type="number"
												bind:value={ep.port}
												min="1" max="65535"
												class="w-28 font-mono tabular-nums {fieldCls}"
												placeholder="1337"
											/>
											<select bind:value={ep.service} class="flex-1 {fieldCls}">
												<option value="tcp">TCP — nc (netcat)</option>
												<option value="http">HTTP — web browser</option>
											</select>
											{#if newChallenge.exposed_ports.length > 1}
												<button type="button" on:click={() => newChallenge.exposed_ports = newChallenge.exposed_ports.filter((_, idx) => idx !== i)} class="p-1.5 text-stone-500 hover:text-down transition-colors">
													<Icon icon="mdi:close" class="w-4 h-4" />
												</button>
											{/if}
										</div>
									{/each}
								</div>
								<p class="text-stone-500 text-xs mt-1.5">
									Enter the port your container listens on internally (e.g. 5001). Users connect via VPN directly to the container's bridge IP on this port.
									Choose <strong class="text-stone-400">TCP</strong> for netcat-style services or <strong class="text-stone-400">HTTP</strong> for web challenges (shows a clickable URL).
								</p>
							</div>

							<!-- Flags -->
							<div>
								<div class="flex items-center justify-between mb-2">
									<span class="block text-xs font-medium text-stone-400">Flags</span>
									<button
										type="button"
										on:click={() => newChallenge.flags = [...newChallenge.flags, { name: `Flag ${newChallenge.flags.length + 1}`, flag: '', points: 25, flag_type: 'static', dynamic_flag_prefix: '' }]}
										class="text-xs text-stone-400 hover:text-stone-200 transition-colors"
									>+ Add Flag</button>
								</div>
								<div class="space-y-3">
									{#each newChallenge.flags as fl, i}
										<div class="p-3 bg-stone-950 border border-stone-800 rounded-md space-y-2">
											<div class="flex items-center gap-2">
												<input type="text" bind:value={fl.name} class="flex-1 {fieldCls}" placeholder="Flag name" />
												<input type="number" bind:value={fl.points} min="0" class="w-20 tabular-nums {fieldCls}" placeholder="pts" />
												<select bind:value={fl.flag_type} class="{fieldCls}">
													<option value="static">Static</option>
													<option value="regex">Regex</option>
													<option value="dynamic">Dynamic</option>
												</select>
												{#if newChallenge.flags.length > 1}
													<button type="button" on:click={() => newChallenge.flags = newChallenge.flags.filter((_, idx) => idx !== i)} class="p-1 text-stone-500 hover:text-down transition-colors">
														<Icon icon="mdi:close" class="w-4 h-4" />
													</button>
												{/if}
											</div>
											{#if fl.flag_type === 'static'}
												<input type="text" bind:value={fl.flag} class="w-full font-mono {fieldCls}" placeholder="flag&#123;value&#125;" />
											{:else if fl.flag_type === 'regex'}
												<input type="text" bind:value={fl.flag} class="w-full font-mono {fieldCls}" placeholder="H7CTF&#123;[a-f0-9-]+&#125; — container generates flag, regex validates" />
												<p class="text-stone-600 text-xs mt-1">Duplicate submissions across users trigger flag-share alerts in Audit</p>
											{:else}
												<input type="text" bind:value={fl.dynamic_flag_prefix} class="w-full font-mono {fieldCls}" placeholder="Prefix (e.g. H7CTF) — generates H7CTF&#123;uuid&#125; per user" />
											{/if}
										</div>
									{/each}
								</div>
							</div>

							<!-- Timer & Limits -->
							<div>
								<span class="block text-xs font-medium text-stone-400 mb-3">Instance Settings</span>
								<div class="grid grid-cols-3 gap-3">
									<label class="block">
										<span class="block text-stone-500 text-xs mb-1">Timeout (min)</span>
										<input type="number" bind:value={newChallenge.instance_timeout} min="1" class="w-full {fieldCls} tabular-nums" />
									</label>
									<label class="block">
										<span class="block text-stone-500 text-xs mb-1">Max Extensions</span>
										<input type="number" bind:value={newChallenge.max_extensions} min="0" class="w-full {fieldCls} tabular-nums" />
									</label>
									<label class="block">
										<span class="block text-stone-500 text-xs mb-1">Cooldown (min)</span>
										<input type="number" bind:value={newChallenge.cooldown_minutes} min="0" class="w-full {fieldCls} tabular-nums" />
									</label>
								</div>
								<p class="text-stone-500 text-xs mt-1.5">How long the instance runs, how many extensions, and cooldown between resets</p>
							</div>
						</div>
					{:else}
						<div class="pt-4 border-t border-stone-800 space-y-5">
							<!-- VM Source Selection -->
							<div>
								<span class={labelCls}>VM Source</span>
								<div class="flex gap-1 p-1 bg-stone-950 border border-stone-800 rounded-md">
									<button
										type="button"
										on:click={() => newChallenge.vm_source = 'template'}
										class="flex-1 inline-flex items-center justify-center py-2 px-3 rounded text-sm leading-none font-medium transition-colors {newChallenge.vm_source === 'template' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
									>
										<Icon icon="mdi:harddisk" class="w-3.5 h-3.5 shrink-0 mr-1" />
										Use Existing Template
									</button>
									<button
										type="button"
										on:click={() => newChallenge.vm_source = 'upload'}
										class="flex-1 inline-flex items-center justify-center py-2 px-3 rounded text-sm leading-none font-medium transition-colors {newChallenge.vm_source === 'upload' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
									>
										<Icon icon="mdi:cloud-upload" class="w-3.5 h-3.5 shrink-0 mr-1" />
										Upload New OVA
									</button>
								</div>
							</div>

							{#if newChallenge.vm_source === 'template'}
								<!-- Template Selector -->
								<div>
									<span class={labelCls}>Select VM Template *</span>
									{#if templates.length === 0}
										<div class="p-4 bg-stone-900/40 border border-stone-800 rounded-md text-center">
											<Icon icon="mdi:alert-circle-outline" class="w-8 h-8 text-stone-500 mx-auto mb-2" />
											<p class="text-stone-400 text-sm">No templates available</p>
											<p class="text-stone-500 text-xs mt-1">Upload a template first or switch to OVA upload</p>
										</div>
									{:else}
										<div class="space-y-2 max-h-48 overflow-y-auto">
											{#each templates as template}
												<button
													type="button"
													on:click={() => newChallenge.vm_template_id = template.id}
													class="w-full p-3 rounded-md border text-left transition-colors {newChallenge.vm_template_id === template.id ? 'bg-stone-800/40 border-stone-600' : 'bg-stone-950 border-stone-800 hover:border-stone-700'}"
												>
													<div class="flex items-center justify-between">
														<div>
															<span class="text-stone-200 font-medium">{template.name}</span>
															<p class="text-stone-500 text-xs mt-0.5 tabular-nums">
																{((template.image_size_bytes || 0) / 1024 / 1024 / 1024).toFixed(1)} GB • {template.vcpu || 1} vCPU • {template.memory_mb || 1024} MB
															</p>
														</div>
														{#if newChallenge.vm_template_id === template.id}
															<Icon icon="mdi:check-circle" class="w-5 h-5 text-up" />
														{/if}
													</div>
												</button>
											{/each}
										</div>
									{/if}
								</div>
							{:else}
								<!-- OVA Upload -->
								<div>
									<span class={labelCls}>OVA File *</span>
									<div class="border-2 border-dashed border-stone-700 rounded-md p-6 text-center hover:border-stone-600 transition-colors">
										{#if ovaFile}
											<div class="flex items-center justify-center gap-3">
												<Icon icon="mdi:file-check-outline" class="w-8 h-8 text-up" />
												<div class="text-left">
													<p class="text-stone-200 font-medium">{ovaFile.name}</p>
													<p class="text-stone-500 text-sm tabular-nums">{(ovaFile.size / 1024 / 1024 / 1024).toFixed(2)} GB</p>
												</div>
												<button type="button" on:click={() => ovaFile = null} class="text-down hover:text-down/80 p-2">
													<Icon icon="mdi:close" class="w-5 h-5" />
												</button>
											</div>
										{:else}
											<Icon icon="mdi:cloud-upload" class="w-12 h-12 text-stone-600 mx-auto mb-3" />
											<p class="text-stone-400 mb-2">Drop your OVA file here or click to browse</p>
											<input type="file" accept=".ova,.qcow2,.vmdk" on:change={handleOvaUpload} class="hidden" id="ova-upload" />
											<label for="ova-upload" class="inline-block px-4 py-2 border border-stone-700 text-stone-300 rounded-md cursor-pointer hover:bg-stone-800/40 transition-colors text-sm">
												Select File
											</label>
										{/if}
									</div>
									<p class="text-stone-500 text-xs mt-2">Supported: .ova, .qcow2, .vmdk (max 20GB)</p>
								</div>
							{/if}

							<!-- Multiple Flags -->
							<div>
								<div class="flex items-center justify-between mb-3">
									<span class="block text-xs font-medium text-stone-400">Flags ({newChallenge.flags.length})</span>
									<button type="button" on:click={addFlag} class="text-sm leading-none text-stone-400 hover:text-stone-200 flex items-center gap-1 transition-colors">
										<Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />
										Add Flag
									</button>
								</div>
								<div class="space-y-3">
									{#each newChallenge.flags as flag, i}
										<div class="bg-stone-950 border border-stone-800 rounded-md p-4">
											<div class="flex items-center gap-3 mb-3">
												<input
													type="text"
													bind:value={flag.name}
													class="flex-1 {fieldCls}"
													placeholder="Flag name (e.g., User Flag)"
												/>
												<input
													type="number"
													bind:value={flag.points}
													min="1"
													class="w-24 text-center tabular-nums {fieldCls}"
													placeholder="Points"
												/>
												{#if newChallenge.flags.length > 1}
													<button type="button" on:click={() => removeFlag(i)} class="text-down hover:text-down/80 p-2 transition-colors">
														<Icon icon="mdi:trash-can-outline" class="w-5 h-5" />
													</button>
												{/if}
											</div>
											<input
												type="text"
												bind:value={flag.flag}
												required
												class="w-full font-mono {fieldCls}"
												placeholder="flag&#123;...&#125;"
											/>
										</div>
									{/each}
								</div>
								<p class="text-stone-500 text-xs mt-2 tabular-nums">Total points: {newChallenge.flags.reduce((sum, f) => sum + (f.points || 0), 0)}</p>
							</div>
						</div>
					{/if}

					<!-- File Attachments -->
					<div class="pt-4 border-t border-stone-800 space-y-3">
						<div class="flex items-center justify-between">
							<span class="block text-xs font-medium text-stone-400">File Attachments <span class="text-stone-500 font-normal">(optional)</span></span>
							<label class="text-xs leading-none text-stone-400 hover:text-stone-200 transition-colors cursor-pointer flex items-center gap-1">
								<Icon icon="mdi:paperclip" class="w-3 h-3 shrink-0" />
								Add Files
								<input type="file" multiple on:change={addPendingAttachment} class="hidden" />
							</label>
						</div>
						{#if pendingAttachments.length > 0}
							<div class="space-y-2">
								{#each pendingAttachments as attachment, i}
									<div class="flex items-center gap-2 p-2 bg-stone-950 border border-stone-800 rounded-md">
										<Icon icon="mdi:file-outline" class="w-4 h-4 text-stone-400 shrink-0" />
										<div class="min-w-0 flex-1">
											<p class="text-xs text-stone-300 truncate">{attachment.file.name}</p>
											<p class="text-xs text-stone-600 tabular-nums">{formatFileSize(attachment.file.size)}</p>
										</div>
										<input
											type="text"
											bind:value={attachment.description}
											placeholder="Description (optional)"
											class="flex-1 min-w-0 px-2 py-1 bg-stone-950 border border-stone-800 rounded text-xs text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-500"
										/>
										<button
											type="button"
											on:click={() => removePendingAttachment(i)}
											class="p-1 text-stone-500 hover:text-down transition-colors shrink-0"
										>
											<Icon icon="mdi:close" class="w-3.5 h-3.5" />
										</button>
									</div>
								{/each}
							</div>
						{:else}
							<p class="text-xs text-stone-600">No files selected. Files can also be added after creating the challenge.</p>
						{/if}
						{#if attachmentUploadStatus}
								<p class="text-xs leading-none text-stone-400 flex items-center gap-1.5">
									<Icon icon="mdi:loading" class="w-3 h-3 shrink-0 animate-spin" />
								{attachmentUploadStatus}
							</p>
						{/if}
					</div>

					<!-- Actions -->
					<div class="flex gap-3 pt-4">
						<button type="submit" disabled={uploadLoading} class="flex-1 {btnPrimary}">
							{#if uploadLoading}
									<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
								{#if attachmentUploadStatus}
									Uploading files...
								{:else if uploadProgress > 0}
									Uploading {uploadProgress}%
								{:else}
									Creating...
								{/if}
							{:else}
									<Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />
								Create Challenge
							{/if}
						</button>
						<button
							type="button"
							on:click={() => showCreateModal = false}
							disabled={uploadLoading}
							class="px-4 py-2 rounded-md text-stone-400 text-sm font-medium hover:text-stone-200 hover:bg-stone-800/40 transition-colors disabled:opacity-50"
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
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => { showEditModal = false; editingChallenge = null; }}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-2xl max-h-[90vh] overflow-y-auto" role="dialog" aria-modal="true">
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between sticky top-0 bg-stone-950 z-10">
				<div>
					<h2 class="text-lg font-semibold text-stone-100">Edit Challenge</h2>
					<p class="text-xs text-stone-500 mt-0.5">
						{editingChallenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker'} ·
						<span class="{editingChallenge.status === 'published' ? 'text-up' : 'text-warn'}">{editingChallenge.status}</span>
						· {editingChallenge.slug}
					</p>
				</div>
				<button on:click={() => { showEditModal = false; editingChallenge = null; }} class="text-stone-500 hover:text-stone-200 transition-colors">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={handleEditChallenge} class="p-6 space-y-5">

				<!-- ── Core ─────────────────────────────────────── -->
				<label class="block">
					<span class={labelCls}>Name *</span>
					<input type="text" bind:value={editingChallenge.name} required class="w-full {fieldCls}" />
				</label>

				<label class="block">
					<span class={labelCls}>Description</span>
					<textarea bind:value={editingChallenge.description} rows="4" class="w-full {fieldCls} resize-none"></textarea>
				</label>

				<!-- ── Category / Difficulty / Points / Author ─── -->
				<div class="grid grid-cols-2 gap-4">
					<label class="block">
						<span class={labelCls}>Category</span>
						{#if categories.length > 0}
							<select bind:value={editingChallenge.category_id} class="w-full {fieldCls}">
								<option value="">Uncategorised</option>
								{#each categories as cat}
									<option value={cat.id}>{cat.name}</option>
								{/each}
							</select>
						{:else}
							<input type="text" bind:value={editingChallenge.category_name} placeholder="Category name" class="w-full {fieldCls}" />
						{/if}
					</label>
					<label class="block">
						<span class={labelCls}>Difficulty</span>
						<select bind:value={editingChallenge.difficulty} class="w-full {fieldCls}">
							<option value="easy">Easy</option>
							<option value="medium">Medium</option>
							<option value="hard">Hard</option>
							<option value="insane">Insane</option>
						</select>
					</label>
					<label class="block">
						<span class={labelCls}>Points</span>
						<input type="number" bind:value={editingChallenge.base_points} required min="1" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Author Name</span>
						<input type="text" bind:value={editingChallenge.author_name} placeholder="e.g. abu" class="w-full {fieldCls}" />
					</label>
				</div>

				<!-- ── Docker-specific ───────────────────────────── -->
				{#if editingChallenge.resource_type !== 'vm'}
					<div class="border border-stone-800 rounded-lg p-4 space-y-4">
						<h3 class="text-xs font-semibold text-stone-400">Container Settings</h3>

						<div class="grid grid-cols-3 gap-3">
							<label class="block col-span-2">
								<span class={labelCls}>Docker Image</span>
								<input type="text" bind:value={editingChallenge.container_image} placeholder="ghcr.io/org/image" class="w-full font-mono {fieldCls}" />
							</label>
							<label class="block">
								<span class={labelCls}>Tag</span>
								<input type="text" bind:value={editingChallenge.container_tag} placeholder="latest" class="w-full font-mono {fieldCls}" />
							</label>
						</div>

						<div class="grid grid-cols-3 gap-3">
							<label class="block">
								<span class={labelCls}>Platform</span>
								<input type="text" bind:value={editingChallenge.container_platform} placeholder="linux/amd64" class="w-full font-mono {fieldCls}" />
							</label>
							<label class="block">
								<span class={labelCls}>CPU Limit</span>
								<input type="text" bind:value={editingChallenge.cpu_limit} placeholder="1" class="w-full {fieldCls}" />
							</label>
							<label class="block">
								<span class={labelCls}>Memory Limit</span>
								<input type="text" bind:value={editingChallenge.memory_limit} placeholder="512m" class="w-full {fieldCls}" />
							</label>
						</div>

						<!-- Exposed Ports -->
						<div>
							<div class="flex items-center justify-between mb-2">
								<span class="block text-xs font-medium text-stone-400">Exposed Ports</span>
								<button
									type="button"
									on:click={() => { editingChallenge.exposed_ports = [...(editingChallenge.exposed_ports || []), { port: 0, protocol: 'tcp', service: 'tcp' }]; }}
									class="text-xs leading-none text-stone-400 hover:text-stone-200 transition-colors flex items-center gap-1"
								>
									<Icon icon="mdi:plus" class="w-3 h-3 shrink-0" /> Add Port
								</button>
							</div>
							{#each (editingChallenge.exposed_ports || []) as ep, i}
								<div class="flex items-center gap-2 mb-2">
									<input type="number" bind:value={ep.port} placeholder="Port" min="1" max="65535" class="w-20 font-mono tabular-nums {fieldCls}" />
									<select bind:value={ep.service} class="flex-1 {fieldCls}">
										<option value="tcp">TCP — nc (netcat)</option>
										<option value="http">HTTP — web browser</option>
									</select>
									<button
										type="button"
										on:click={() => removeEditPort(i)}
										class="p-1 text-stone-600 hover:text-down transition-colors"
									>
										<Icon icon="mdi:close" class="w-3.5 h-3.5" />
									</button>
								</div>
							{/each}
							<p class="text-stone-600 text-xs mt-1">Port your container listens on internally. TCP shows <code class="text-stone-500">nc host port</code>; HTTP shows a clickable URL.</p>
						</div>
					</div>
				{/if}

				<!-- ── VM-specific ───────────────────────────────── -->
				{#if editingChallenge.resource_type === 'vm'}
					<div class="border border-stone-800 rounded-lg p-4">
						<h3 class="text-xs font-semibold text-stone-400 mb-3">VM Settings</h3>
						<label class="block">
							<span class={labelCls}>VM Template</span>
							<select bind:value={editingChallenge.vm_template_id} class="w-full {fieldCls}">
								<option value="">No template / unchanged</option>
								{#each templates as template}
									<option value={template.id}>{template.name} ({template.vcpu}vCPU / {template.memory_mb}MB)</option>
								{/each}
							</select>
						</label>
					</div>
				{/if}

				<!-- ── Timer / Instance Settings ─────────────────── -->
				<div class="border border-stone-800 rounded-lg p-4 space-y-3">
					<h3 class="text-xs font-semibold text-stone-400">Instance Settings</h3>
					<div class="grid grid-cols-3 gap-3">
						<label class="block">
							<span class={labelCls}>Timeout (min)</span>
							<input type="number" bind:value={editingChallenge.instance_timeout} min="1" class="w-full {fieldCls} tabular-nums" />
						</label>
						<label class="block">
							<span class={labelCls}>Max Extensions</span>
							<input type="number" bind:value={editingChallenge.max_extensions} min="0" class="w-full {fieldCls} tabular-nums" />
						</label>
						<label class="block">
							<span class={labelCls}>Cooldown (min)</span>
							<input type="number" bind:value={editingChallenge.cooldown_minutes} min="0" class="w-full {fieldCls} tabular-nums" />
						</label>
					</div>
				</div>

				<!-- ── Actions ───────────────────────────────────── -->
				<div class="flex gap-3 pt-2">
					<button type="submit" disabled={actionLoading === editingChallenge.id} class="flex-1 {btnPrimary}">
						{actionLoading === editingChallenge.id ? 'Saving...' : 'Save Changes'}
					</button>
					<button
						type="button"
						on:click={() => { showEditModal = false; editingChallenge = null; }}
						class="px-4 py-2 rounded-md text-stone-400 text-sm font-medium hover:text-stone-200 hover:bg-stone-800/40 transition-colors"
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
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showNodeModal = false}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-lg" role="dialog" aria-modal="true">
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between">
				<h2 class="text-lg font-semibold text-stone-100">Add VM Node</h2>
				<button on:click={() => showNodeModal = false} class="text-stone-500 hover:text-stone-200 transition-colors">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={createNode} class="p-6 space-y-4">
				<div class="grid grid-cols-2 gap-4">
					<label class="block col-span-2">
						<span class={labelCls}>Node Name</span>
						<input type="text" bind:value={newNode.name} required placeholder="prod-node-01" class="w-full {fieldCls}" />
					</label>
					<label class="block">
						<span class={labelCls}>Hostname</span>
						<input type="text" bind:value={newNode.hostname} required placeholder="vm-node.example.com" class="w-full {fieldCls}" />
					</label>
					<label class="block">
						<span class={labelCls}>IP Address</span>
						<input type="text" bind:value={newNode.ip_address} required placeholder="10.0.0.1" class="w-full font-mono {fieldCls}" />
					</label>
				</div>

				<div class="grid grid-cols-3 gap-4">
					<label class="block">
						<span class={labelCls}>Total vCPU</span>
						<input type="number" bind:value={newNode.total_vcpu} required min="1" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Memory (MB)</span>
						<input type="number" bind:value={newNode.total_memory_mb} required min="1024" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Disk (GB)</span>
						<input type="number" bind:value={newNode.total_disk_gb} required min="10" class="w-full {fieldCls} tabular-nums" />
					</label>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<label class="block">
						<span class={labelCls}>Max VMs</span>
						<input type="number" bind:value={newNode.max_vms} required min="1" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Provider</span>
						<select bind:value={newNode.provider} class="w-full {fieldCls}">
							<option value="gcp">Google Cloud</option>
							<option value="aws">AWS</option>
							<option value="azure">Azure</option>
							<option value="bare-metal">Bare Metal</option>
						</select>
					</label>
				</div>

				<div class="flex gap-3 pt-2">
					<button type="submit" disabled={actionLoading === 'create-node'} class="flex-1 {btnPrimary}">
						{actionLoading === 'create-node' ? 'Creating...' : 'Add Node'}
					</button>
					<button
						type="button"
						on:click={() => showNodeModal = false}
						class="px-4 py-2 rounded-md text-stone-400 text-sm font-medium hover:text-stone-200 hover:bg-stone-800/40 transition-colors"
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
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showTemplateUploadModal = false}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-lg" role="dialog" aria-modal="true">
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between">
				<h2 class="text-lg font-semibold text-stone-100">Upload VM Template</h2>
				<button on:click={() => showTemplateUploadModal = false} class="text-stone-500 hover:text-stone-200 transition-colors">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<form on:submit|preventDefault={uploadTemplate} class="p-6 space-y-4">
				{#if templateUploading && templateUploadProgress > 0}
					<div class="py-3 px-4 bg-stone-900/50 rounded-md border border-stone-800">
						<div class="flex items-center justify-between mb-2">
							<span class="text-sm text-stone-300">Uploading...</span>
							<span class="text-sm text-stone-400 tabular-nums">{templateUploadProgress}%</span>
						</div>
						<div class="w-full bg-stone-800 rounded-full h-2 overflow-hidden">
							<div class="bg-amber-500/70 h-full transition-all" style="width: {templateUploadProgress}%"></div>
						</div>
					</div>
				{/if}

				<label class="block">
					<span class={labelCls}>Template Name</span>
					<input type="text" bind:value={templateName} required placeholder="moby-dock" class="w-full {fieldCls}" />
				</label>

				<label class="block">
					<span class={labelCls}>Description</span>
					<textarea bind:value={templateDescription} rows="2" placeholder="Docker escape challenge..." class="w-full {fieldCls} resize-none"></textarea>
				</label>

				<div class="grid grid-cols-2 gap-4">
					<label class="block">
						<span class={labelCls}>Min vCPU</span>
						<input type="number" bind:value={templateMinVcpu} min="1" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Min Memory (MB)</span>
						<input type="number" bind:value={templateMinMemory} min="512" class="w-full {fieldCls} tabular-nums" />
					</label>
				</div>

				<div>
					<span class={labelCls}>OVA/QCOW2/VMDK File</span>
					<div class="border-2 border-dashed border-stone-700 rounded-md p-6 text-center hover:border-stone-600 transition-colors">
						{#if templateFile}
							<div class="flex items-center justify-center gap-3">
								<Icon icon="mdi:file-check-outline" class="w-6 h-6 text-up" />
								<div class="text-left">
									<p class="text-stone-200 text-sm">{templateFile.name}</p>
									<p class="text-stone-500 text-xs tabular-nums">{(templateFile.size / 1024 / 1024 / 1024).toFixed(2)} GB</p>
								</div>
								<button type="button" on:click={() => templateFile = null} class="text-down hover:text-down/80 p-1">
									<Icon icon="mdi:close" class="w-4 h-4" />
								</button>
							</div>
						{:else}
							<Icon icon="mdi:cloud-upload" class="w-10 h-10 text-stone-600 mx-auto mb-2" />
							<p class="text-stone-400 text-sm mb-2">Drop file or click to browse</p>
							<input
								type="file"
								accept=".ova,.qcow2,.vmdk"
								on:change={(e) => templateFile = e.currentTarget.files?.[0] || null}
								class="hidden"
								id="template-upload"
							/>
							<label for="template-upload" class="inline-block px-3 py-1.5 border border-stone-700 text-stone-300 rounded-md text-xs cursor-pointer hover:bg-stone-800/40 transition-colors">
								Select File
							</label>
						{/if}
					</div>
				</div>

				<div class="flex gap-3 pt-2">
					<button type="submit" disabled={templateUploading || !templateFile || !templateName} class="flex-1 {btnPrimary}">
						{templateUploading ? 'Uploading...' : 'Upload Template'}
					</button>
					<button
						type="button"
						on:click={() => showTemplateUploadModal = false}
						disabled={templateUploading}
						class="px-4 py-2 rounded-md text-stone-400 text-sm font-medium hover:text-stone-200 hover:bg-stone-800/40 transition-colors disabled:opacity-50"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}
