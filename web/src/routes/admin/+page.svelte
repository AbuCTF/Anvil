<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$api';
	import Icon from '@iconify/svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { difficultyClass, resourceClass, resourceIcon, resourceLabel } from '$lib/rank';
	import { formatLocalDateLong, formatLocalDateTimeWithZone, instantTitle, viewerTimeZone } from '$lib/time';
	import { confirmDialog, alertDialog, promptDialog } from '$lib/stores/dialog';

	let activeTab = 'overview';
	let loading = true;
	let stats: any = null;
	let users: any[] = [];
	let challenges: any[] = [];
	let error = '';
	let categoriesError = '';
	let showCreateModal = false;
	let showEditModal = false;
	let editingChallenge: any = null;
	let actionLoading = '';

	let infraStats: any = null;
	let nodes: any[] = [];
	let templates: any[] = [];
	let activeInstances: any[] = [];
	let activeDockerInstances: any[] = [];
	let infrastructureError = '';

	let intelLoading = false;
	let flagShares: any[] = [];
	let instanceFlags: any[] = [];
	let intelError = '';

	let teams: any[] = [];
	let teamsLoading = false;
	let teamsError = '';
	let teamsQuery = '';
	let teamsSort = 'score';
	let expandedTeams: Record<string, boolean> = {};
	let addMemberInput: Record<string, string> = {};
	let expandedSolves: Record<string, boolean> = {};
	let teamSolves: Record<string, any[]> = {};
	let solvesLoading: Record<string, boolean> = {};

	let showNodeModal = false;
	let showTemplateUploadModal = false;

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

	let templateFile: File | null = null;
	let templateName = '';
	let templateDescription = '';
	let templateMinVcpu = 1;
	let templateMinMemory = 1024;
	let templateUploadProgress = 0;
	let templateUploading = false;

	let platformSettings: Record<string, any> = {};
	let savingSettings = false;
	let settingsChanged = false;
	let settingsError = '';
	let eventWindowError = '';
	let browserTimeZone = 'local time';

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

	interface PendingAttachment {
		file: File;
		description: string;
	}
	let pendingAttachments: PendingAttachment[] = [];
	let attachmentUploadStatus = '';

	// shared design-system class tokens (see DESIGN.md).
	const fieldCls = 'px-3 py-2 bg-stone-950 border border-stone-800 rounded-md text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-500 transition-colors';
	const labelCls = 'metadata-label block text-stone-400 mb-1.5';
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
			// reset the input so the same file can be re-added if needed
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
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to publish' });
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
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to unpublish' });
		} finally {
			actionLoading = '';
		}
	}

	async function deleteChallenge(challenge: any) {
		if (!(await confirmDialog({ title: 'Delete challenge', message: `Delete "${challenge.name}"? This cannot be undone.`, confirmLabel: 'Delete', danger: true }))) return;
		actionLoading = challenge.id;
		try {
			await api.deleteAdminChallenge(challenge.id);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to delete' });
		} finally {
			actionLoading = '';
		}
	}

	function openEditModal(challenge: any) {
		editingChallenge = {
			...challenge,
			// ensure arrays/optional fields have proper defaults for the form
			exposed_ports: challenge.exposed_ports || [],
			instance_timeout: challenge.instance_timeout ?? 120,
			max_extensions: challenge.max_extensions ?? 3,
			cooldown_minutes: challenge.cooldown_minutes ?? 15,
			container_image: challenge.container_image || '',
			container_tag: challenge.container_tag || 'latest',
			container_platform: challenge.container_platform || '',
			cpu_limit: challenge.cpu_limit || '1',
			memory_limit: challenge.memory_limit || '512Mi',
			author_name: challenge.author_name || '',
			category_id: challenge.category_id || '',
		};
		editTab = 'settings';
		editFlags = []; editHints = []; editAttachments = [];
		subError = ''; subNote = '';
		showEditModal = true;
		void loadEditSubdata(challenge.id);
	}

	// --- edit modal: flags / hints / files management (CTFd-parity, inline CRUD) ---
	let editTab: 'settings' | 'flags' | 'hints' | 'files' = 'settings';
	let editFlags: any[] = [];
	let editHints: any[] = [];
	let editAttachments: any[] = [];
	let subLoading = false;
	let subUploading = false;
	let subError = '';
	let subNote = '';
	let savingSub = '';

	async function loadEditSubdata(id: string) {
		subLoading = true; subError = '';
		try {
			const [f, h, a] = await Promise.all([
				api.getChallengeFlags(id),
				api.getAdminHints(id),
				api.listAttachments(id)
			]);
			editFlags = ((f?.flags ?? f ?? []) as any[]).map((x) => ({ ...x }));
			editHints = ((h?.hints ?? h ?? []) as any[]).map((x) => ({ ...x }));
			editAttachments = (a?.attachments ?? []) as any[];
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to load challenge data';
		} finally {
			subLoading = false;
		}
	}

	function flash(msg: string) { subNote = msg; subError = ''; setTimeout(() => { if (subNote === msg) subNote = ''; }, 2500); }

	function addEditFlag() {
		editFlags = [...editFlags, { name: `Flag ${editFlags.length + 1}`, flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '', has_value: false }];
	}
	async function saveEditFlag(f: any, i: number) {
		subError = ''; savingSub = 'flag' + i;
		const body: any = { name: f.name, points: Number(f.points) || 0, flag_type: f.flag_type || 'static', dynamic_flag_prefix: f.dynamic_flag_prefix || '' };
		// only send the value when one was typed — blank keeps the stored flag (update) and is required on create
		if (f.flag) body.flag = f.flag;
		try {
			if (f.id) {
				await api.updateFlag(editingChallenge.id, f.id, body);
			} else {
				if (f.flag_type !== 'dynamic' && !f.flag) { subError = 'Flag value is required'; savingSub = ''; return; }
				const res = await api.createFlag(editingChallenge.id, { ...body, flag: f.flag || '' });
				f.id = res?.id ?? res?.flag?.id;
			}
			f.flag = ''; f.has_value = f.flag_type === 'dynamic' ? false : true; editFlags = editFlags;
			flash('Flag saved');
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to save flag';
		} finally { savingSub = ''; }
	}
	async function deleteEditFlag(f: any, i: number) {
		subError = '';
		if (f.id) {
			try { await api.deleteFlag(editingChallenge.id, f.id); }
			catch (e) { subError = e instanceof Error ? e.message : 'Failed to delete flag'; return; }
		}
		editFlags = editFlags.filter((_, idx) => idx !== i);
		flash('Flag removed');
	}

	function addEditHint() {
		editHints = [...editHints, { content: '', cost: 0 }];
	}
	async function saveEditHint(hnt: any, i: number) {
		subError = ''; savingSub = 'hint' + i;
		const body = { content: hnt.content, cost: Number(hnt.cost) || 0 };
		try {
			if (!hnt.content?.trim()) { subError = 'Hint content is required'; savingSub = ''; return; }
			if (hnt.id) {
				await api.updateHint(editingChallenge.id, hnt.id, body);
			} else {
				const res = await api.createHint(editingChallenge.id, body);
				hnt.id = res?.id ?? res?.hint?.id;
			}
			editHints = editHints;
			flash('Hint saved');
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to save hint';
		} finally { savingSub = ''; }
	}
	async function deleteEditHint(hnt: any, i: number) {
		subError = '';
		if (hnt.id) {
			try { await api.deleteHint(editingChallenge.id, hnt.id); }
			catch (e) { subError = e instanceof Error ? e.message : 'Failed to delete hint'; return; }
		}
		editHints = editHints.filter((_, idx) => idx !== i);
		flash('Hint removed');
	}

	async function uploadEditAttachment(event: Event) {
		const input = event.target as HTMLInputElement;
		if (!input.files?.length) return;
		subError = ''; subUploading = true;
		try {
			for (const file of Array.from(input.files)) {
				const fd = new FormData();
				fd.append('file', file);
				await api.uploadAttachment(editingChallenge.id, fd);
			}
			const a = await api.listAttachments(editingChallenge.id);
			editAttachments = (a?.attachments ?? []) as any[];
			flash('File uploaded');
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to upload file';
		} finally {
			subUploading = false;
			input.value = '';
		}
	}
	async function deleteEditAttachment(a: any) {
		subError = '';
		try {
			await api.deleteAttachment(editingChallenge.id, a.id);
			editAttachments = editAttachments.filter((x) => x.id !== a.id);
			flash('File removed');
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to delete file';
		}
	}

	function humanSize(bytes: number): string {
		if (!bytes) return '0 B';
		const u = ['B', 'KB', 'MB', 'GB'];
		const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), u.length - 1);
		return `${(bytes / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${u[i]}`;
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

			// category: send category_id (may be empty string to clear it) or fall back to name
			if (categories.length > 0) {
				payload.category_id = editingChallenge.category_id || null;
			} else if (editingChallenge.category_name) {
				payload.category = editingChallenge.category_name;
			}

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

			if (editingChallenge.resource_type === 'vm' && editingChallenge.vm_template_id) {
				payload.vm_template_id = editingChallenge.vm_template_id;
			}

			await api.updateAdminChallenge(editingChallenge.id, payload);
			await loadDashboard();
			showEditModal = false;
			editingChallenge = null;
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to update' });
		} finally {
			actionLoading = '';
		}
	}

	onMount(async () => {
		browserTimeZone = viewerTimeZone();
		await loadDashboard();
	});

	async function loadDashboard() {
		loading = true;
		categoriesError = '';
		try {
			const [statsRes, usersRes, challengesRes, categoriesRes] = await Promise.all([
				api.getAdminStats(),
				api.getAdminUsers(),
				api.getAdminChallenges(),
				api.getAdminCategories().catch((e) => {
					categoriesError = e instanceof Error ? e.message : 'Failed to load categories';
					return { categories: [] };
				})
			]);
			stats = statsRes;
			users = usersRes.users || [];
			challenges = challengesRes.challenges || [];
			categories = categoriesRes.categories || [];
			error = '';

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
				infrastructureError = '';
			} catch (e) {
				infrastructureError = e instanceof Error ? e.message : 'Failed to load infrastructure data';
				infraStats = null;
				nodes = [];
				templates = [];
				activeInstances = [];
				activeDockerInstances = [];
			}

			try {
				const settingsRes = await api.getPlatformSettings();
				platformSettings = settingsRes.settings || {};
				settingsError = '';
			} catch (e) {
				settingsError = e instanceof Error ? e.message : 'Failed to load platform settings';
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
		intelError = '';
		try {
			const [sharesRes, flagsRes] = await Promise.all([
				api.getFlagShares(),
				api.getInstanceFlags()
			]);
			flagShares = sharesRes.flag_shares || [];
			instanceFlags = flagsRes.instance_flags || [];
		} catch (e) {
			intelError = e instanceof Error ? e.message : 'Failed to load audit data';
		} finally {
			intelLoading = false;
		}
	}

	function setTab(id: string) {
		activeTab = id;
		if (id === 'intel' && flagShares.length === 0 && instanceFlags.length === 0) {
			loadIntel();
		}
		if (id === 'teams' && teams.length === 0) {
			loadTeams();
		}
	}

	async function savePlatformSettings() {
		if (settingsError || eventWindowError) return;
		savingSettings = true;
		try {
			await api.updatePlatformSettings(platformSettings);
			settingsChanged = false;
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to save settings' });
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

	function datetimeLocalValue(value: unknown): string {
		if (typeof value !== 'string' || !value) return '';
		const date = new Date(value);
		if (!Number.isFinite(date.getTime())) return '';
		const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
		return local.toISOString().slice(0, 16);
	}

	function handleEventTimeInput(e: Event, key: 'event.start_at' | 'event.end_at') {
		const value = (e.target as HTMLInputElement).value;
		updateSetting(key, value ? new Date(value).toISOString() : '');
	}

	function validateEventWindow(startValue: unknown, endValue: unknown): string {
		const start = typeof startValue === 'string' ? startValue : '';
		const end = typeof endValue === 'string' ? endValue : '';
		if (!start && !end) return '';
		if (!start || !end) return 'Set both times, or clear both to hide the event clock.';
		const startAt = Date.parse(start);
		const endAt = Date.parse(end);
		if (!Number.isFinite(startAt) || !Number.isFinite(endAt)) return 'Enter a valid event window.';
		if (endAt <= startAt) return 'The event must end after it starts.';
		return '';
	}

	$: eventWindowError = validateEventWindow(platformSettings['event.start_at'], platformSettings['event.end_at']);

	function numberSetting(key: string, fallback: number, min: number, max: number): number {
		const value = Number(platformSettings[key]);
		if (!Number.isFinite(value)) return fallback;
		return Math.min(max, Math.max(min, Math.round(value)));
	}

	function applyChallengeDefaults(difficulty = newChallenge.difficulty) {
		newChallenge = {
			...newChallenge,
			difficulty,
			instance_timeout: numberSetting('instance.default_timeout_minutes', 60, 1, 1440),
			max_extensions: numberSetting('instance.max_extensions', 3, 0, 10),
			vm_timeout_minutes: numberSetting(`vm_default_timeout_${difficulty}`, 120, 30, 480),
			vm_max_extensions: numberSetting('instance.max_extensions', 3, 0, 10),
			vm_extension_minutes: numberSetting('instance.extension_minutes', 30, 1, 240),
			cooldown_minutes: numberSetting(`cooldown.${difficulty}_minutes`, 10, 0, 120)
		};
	}

	function openCreateChallenge() {
		applyChallengeDefaults();
		showCreateModal = true;
	}

	function changeChallengeDifficulty(e: Event) {
		applyChallengeDefaults((e.target as HTMLSelectElement).value);
	}

	async function createNode() {
		actionLoading = 'create-node';
		try {
			await api.createNode(newNode);
			showNodeModal = false;
			newNode = { name: '', hostname: '', ip_address: '', total_vcpu: 16, total_memory_mb: 61440, total_disk_gb: 100, max_vms: 10, region: '', provider: 'gcp' };
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to create node' });
		} finally {
			actionLoading = '';
		}
	}

	async function deleteNode(nodeId: string) {
		if (!(await confirmDialog({ title: 'Delete node', message: 'Delete this node? This cannot be undone.', confirmLabel: 'Delete', danger: true }))) return;
		actionLoading = nodeId;
		try {
			await api.deleteNode(nodeId);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to delete node' });
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
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to upload template' });
		} finally {
			templateUploading = false;
		}
	}

	async function deleteTemplate(templateId: string) {
		if (!(await confirmDialog({ title: 'Delete template', message: 'Delete this template? Challenges using it will break.', confirmLabel: 'Delete', danger: true }))) return;
		actionLoading = templateId;
		try {
			await api.deleteVMTemplate(templateId);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to delete template' });
		} finally {
			actionLoading = '';
		}
	}

	async function deleteInstance(instanceId: string) {
		if (!(await confirmDialog({ title: 'Terminate instance', message: 'Force stop and remove this VM instance? The user will lose their session.', confirmLabel: 'Terminate', danger: true }))) return;
		actionLoading = instanceId;
		try {
			await api.forceStopAdminInstance(instanceId);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to stop instance' });
		} finally {
			actionLoading = '';
		}
	}

	async function deleteDockerInstance(instanceId: string) {
		if (!(await confirmDialog({ title: 'Terminate instance', message: 'Force stop and remove this Docker instance? The user will lose their session.', confirmLabel: 'Terminate', danger: true }))) return;
		actionLoading = instanceId;
		try {
			await api.forceStopAdminInstance(instanceId);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to stop Docker instance' });
		} finally {
			actionLoading = '';
		}
	}

	async function deleteUser(userId: string) {
		if (!(await confirmDialog({ title: 'Delete user', message: 'Delete this user? This action cannot be undone.', confirmLabel: 'Delete', danger: true }))) return;
		actionLoading = userId;
		try {
			await api.deleteAdminUser(userId);
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to delete user' });
		} finally {
			actionLoading = '';
		}
	}

	async function changeUserRole(userId: string, newRole: string) {
		if (!(await confirmDialog({ message: `Change user role to ${newRole}?` }))) return;
		actionLoading = userId;
		try {
			await api.updateAdminUser(userId, { role: newRole });
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to change user role' });
		} finally {
			actionLoading = '';
		}
	}

	async function toggleBan(user: any) {
		const banned = user.is_banned;
		if (!(await confirmDialog({
			title: banned ? 'Unban user' : 'Ban user',
			message: banned ? `Unban ${user.username}?` : `Ban ${user.username}? They won't be able to sign in.`,
			confirmLabel: banned ? 'Unban' : 'Ban',
			danger: !banned
		}))) return;
		actionLoading = user.id;
		try {
			await (banned ? api.unbanUser(user.id) : api.banUser(user.id));
			await loadDashboard();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to update ban status' });
		} finally {
			actionLoading = '';
		}
	}

	async function loadTeams() {
		teamsLoading = true;
		teamsError = '';
		try {
			const res = await api.getAdminTeams({ q: teamsQuery, sort: teamsSort });
			teams = res.teams || [];
		} catch (e) {
			teamsError = e instanceof Error ? e.message : 'Failed to load teams';
			teams = [];
		} finally {
			teamsLoading = false;
		}
	}

	async function teamUpdate(id: string, data: any) {
		actionLoading = id;
		try {
			await api.updateAdminTeam(id, data);
			await loadTeams();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to update team' });
		} finally {
			actionLoading = '';
		}
	}

	async function renameTeam(team: any) {
		const name = await promptDialog({ message: 'New team name:', defaultValue: team.name });
		if (name === null || name.trim() === team.name) return;
		await teamUpdate(team.id, { name: name.trim() });
	}

	async function editTeamScore(team: any) {
		const raw = await promptDialog({ message: 'Total score:', defaultValue: String(team.total_score ?? 0) });
		if (raw === null) return;
		const score = parseInt(raw.trim(), 10);
		if (Number.isNaN(score)) {
			alertDialog({ title: 'Error', message: 'Score must be an integer' });
			return;
		}
		await teamUpdate(team.id, { total_score: score });
	}

	async function editTeamMax(team: any) {
		const raw = await promptDialog({ message: 'Max members (blank = unlimited):', defaultValue: team.max_members == null ? '' : String(team.max_members) });
		if (raw === null) return;
		const trimmed = raw.trim();
		const max = trimmed === '' ? null : parseInt(trimmed, 10);
		if (max !== null && (Number.isNaN(max) || max < 1)) {
			alertDialog({ title: 'Error', message: 'Max members must be a positive integer or blank' });
			return;
		}
		await teamUpdate(team.id, { max_members: max });
	}

	async function rotateTeamCode(team: any) {
		if (!(await confirmDialog({ message: `Regenerate the join code for ${team.name}? The current code stops working.` }))) return;
		actionLoading = team.id;
		try {
			await api.rotateAdminTeamCode(team.id);
			await loadTeams();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to rotate join code' });
		} finally {
			actionLoading = '';
		}
	}

	async function disbandTeam(team: any) {
		if (!(await confirmDialog({ title: 'Disband team', message: `Disband ${team.name}? Its ${team.member_count} member(s) will be removed. This cannot be undone.`, confirmLabel: 'Disband', danger: true }))) return;
		actionLoading = team.id;
		try {
			await api.deleteAdminTeam(team.id);
			await loadTeams();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to disband team' });
		} finally {
			actionLoading = '';
		}
	}

	async function kickMember(team: any, member: any) {
		if (!(await confirmDialog({ title: 'Remove member', message: `Remove ${member.username} from ${team.name}?`, confirmLabel: 'Remove', danger: true }))) return;
		actionLoading = team.id;
		try {
			await api.removeAdminTeamMember(team.id, member.id);
			await loadTeams();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to remove member' });
		} finally {
			actionLoading = '';
		}
	}

	async function addTeamMember(team: any) {
		const username = (addMemberInput[team.id] || '').trim();
		if (!username) return;
		actionLoading = team.id;
		try {
			await api.addAdminTeamMember(team.id, { username });
			addMemberInput[team.id] = '';
			await loadTeams();
		} catch (e) {
			alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to add member' });
		} finally {
			actionLoading = '';
		}
	}

	function toggleTeamExpand(id: string) {
		expandedTeams[id] = !expandedTeams[id];
		expandedTeams = expandedTeams;
	}

	async function toggleSolvesExpand(id: string) {
		expandedSolves[id] = !expandedSolves[id];
		expandedSolves = expandedSolves;
		if (expandedSolves[id] && teamSolves[id] === undefined) {
			solvesLoading[id] = true;
			solvesLoading = solvesLoading;
			try {
				const res = await api.getAdminTeamSolves(id);
				teamSolves[id] = res.solves || [];
			} catch (e) {
				teamSolves[id] = [];
			} finally {
				solvesLoading[id] = false;
				solvesLoading = solvesLoading;
				teamSolves = teamSolves;
			}
		}
	}

	function formatDate(timestamp: number): string {
		return formatLocalDateLong(timestamp, 'seconds');
	}

	async function handleCreateChallenge() {
		uploadLoading = true;
		uploadError = '';
		uploadProgress = 0;
		attachmentUploadStatus = '';

		// resolve the category: either an existing ID or a new category name
		let categoryId: string | undefined;
		let categoryName: string | undefined;

		if (newChallenge.category_id === '__new__') {
			// new category (the __new__ sentinel): send by name for the backend to resolve
			const trimmed = (newChallenge.newCategoryName || '').trim();
			if (!trimmed) {
				uploadError = 'Please enter a name for the new category.';
				uploadLoading = false;
				return;
			}
			categoryName = trimmed;
		} else if (newChallenge.category_id) {
			categoryId = newChallenge.category_id;
		} else if (newChallenge.category) {
			categoryName = newChallenge.category;
		}

		try {
			let createdChallengeId: string | undefined;

			if (newChallenge.type === 'ova') {
				// OVA challenges must reference an already-converted template (Infrastructure →
				// Templates). Direct-in-modal OVA upload is gone: it produced dead challenges.
				if (newChallenge.vm_source === 'template' && newChallenge.vm_template_id) {
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
				} else {
					throw new Error('Select an existing VM template. To use a new OVA, upload it under Infrastructure → Templates first, then create the challenge here.');
				}
			} else {
				// container and download-only are both resource_type "docker"; a download-only
				// challenge has no image + no ports (files are added as attachments).
				const isDownload = newChallenge.type === 'download';
				const result = await api.createAdminChallenge({
					name: newChallenge.name,
					description: newChallenge.description,
					difficulty: newChallenge.difficulty,
					base_points: newChallenge.base_points,
					...(categoryId ? { category_id: categoryId } : {}),
					...(categoryName ? { category: categoryName } : {}),
					challenge_type: 'docker',
					container_image: isDownload ? '' : newChallenge.docker_image,
					container_platform: isDownload ? '' : newChallenge.container_platform,
					exposed_ports: isDownload ? [] : newChallenge.exposed_ports.filter(p => p.port > 0),
					flags: newChallenge.flags.map((f, i) => ({
						name: f.name,
						flag: f.flag,
						points: f.points,
						sort_order: i + 1,
						flag_type: f.flag_type || 'static',
						dynamic_flag_prefix: f.dynamic_flag_prefix || ''
					})),
					...(isDownload ? {} : {
						instance_timeout: newChallenge.instance_timeout,
						max_extensions: newChallenge.max_extensions,
						cooldown_minutes: newChallenge.cooldown_minutes
					})
				});
				createdChallengeId = result?.id;
			}

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

			// challenge was created — close modal and reset form regardless of attachment failures
			showCreateModal = false;
			await loadDashboard();

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
				// surface file upload failures as a page-level warning so the admin can
				// re-upload from the challenge detail page
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
		{ id: 'teams', label: 'Teams', icon: 'mdi:account-multiple-outline' },
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
	<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10 py-8">
		<PageHeader title="Admin" subtitle="Platform management">
			<div slot="actions">
				{#if activeTab === 'challenges'}
					<button on:click={openCreateChallenge} class={btnPrimary}>
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
			<div class="border-b border-stone-800 mb-8 overflow-x-auto">
				<div class="flex gap-1 min-w-max">
					{#each TABS as tab}
						<button
							type="button"
							on:click={() => setTab(tab.id)}
							class="relative px-3.5 py-2.5 text-sm leading-none font-medium whitespace-nowrap flex items-center gap-2 transition-colors {activeTab === tab.id ? 'text-stone-100' : 'text-stone-500 hover:text-stone-300'}"
						>
							<OpticalIcon icon={tab.icon} size={14} box={14} />
							<span class="optical-label">{tab.label}</span>
							{#if activeTab === tab.id}
								<span class="absolute inset-x-2 -bottom-px h-0.5 bg-stone-400"></span>
							{/if}
						</button>
					{/each}
				</div>
			</div>

			{#if activeTab === 'overview'}
				<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
					{#each [
						{ label: 'Users', value: stats?.total_users || 0 },
						{ label: 'Challenges', value: stats?.total_challenges || 0 },
						{ label: 'Active Instances', value: stats?.active_instances || 0 },
						{ label: 'Total Solves', value: stats?.total_solves || 0 }
					] as stat}
						<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
							<p class="metadata-label text-stone-500">{stat.label}</p>
							<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">{stat.value.toLocaleString()}</p>
						</div>
					{/each}
				</div>

				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
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
										<span class="text-xs text-stone-600 tabular-nums shrink-0" title={instantTitle(user.created_at, 'seconds')}>{formatDate(user.created_at)}</span>
									</div>
								{/each}
							</div>
						{/if}
					</Card>

					<Card title="Top Challenges" bodyClass="">
						{#if challenges.length === 0}
							<EmptyState icon="mdi:flag-outline" text="No challenges yet." />
						{:else}
							<div class="divide-y divide-stone-800/60">
								{#each challenges.slice(0, 5) as challenge}
									<div class="px-4 py-3 flex items-center justify-between gap-3">
										<div class="min-w-0">
											<p class="text-sm text-stone-200 truncate">{challenge.name}</p>
											<span class="inline-flex items-center px-2 py-0.5 mt-1 rounded-full border text-[0.7rem] capitalize {difficultyClass(challenge.difficulty)}"><span class="badge-label">{challenge.difficulty}</span></span>
										</div>
										<span class="text-xs text-stone-400 tabular-nums shrink-0">{challenge.total_solves || 0} solves</span>
									</div>
								{/each}
							</div>
						{/if}
					</Card>
				</div>
			{/if}

			{#if activeTab === 'challenges'}
				{#if categoriesError}
					<div class="mb-6 flex items-center justify-between gap-3 rounded-lg border border-warn/20 bg-warn/5 px-4 py-3 text-sm text-warn" aria-live="polite">
						<span>Categories could not be loaded: {categoriesError}</span>
						<button type="button" on:click={loadDashboard} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
					</div>
				{/if}
				{#if challenges.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:flag-outline" text="No challenges yet.">
							<button on:click={openCreateChallenge} class="mt-3 text-sm text-amber-500/90 hover:text-amber-400 transition-colors">
								Create your first challenge →
							</button>
						</EmptyState>
					</Card>
				{:else}
					<div class="lg:hidden space-y-3">
						{#each challenges as challenge}
							<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
								<div class="flex items-start justify-between gap-3 mb-3">
									<div class="min-w-0">
										<a href="/challenges/{challenge.slug}" class="text-sm font-medium text-stone-200 hover:text-amber-400 transition-colors">{challenge.name}</a>
										<div class="flex items-center flex-wrap gap-1.5 mt-1.5 leading-none">
											<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}"><span class="badge-label">{challenge.difficulty}</span></span>
											<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
												<OpticalIcon icon={resourceIcon(challenge.resource_type)} size={12} box={12} /><span class="badge-label">{resourceLabel(challenge.resource_type)}</span>
											</span>
										</div>
									</div>
									<span class="inline-flex items-center gap-1.5 text-xs leading-none shrink-0 {challenge.status === 'published' ? 'text-up' : 'text-warn'}">
										<span class="w-1.5 h-1.5 rounded-full {challenge.status === 'published' ? 'bg-up' : 'bg-warn'}"></span>
										<span class="optical-label">{challenge.status === 'published' ? 'Published' : 'Draft'}</span>
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

					<div class="hidden lg:block">
						<Card title="Challenges" bodyClass="">
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{challenges.length}</span>
							<div class="overflow-x-auto">
								<table class="w-full min-w-[720px] text-sm">
									<thead>
										<tr class="metadata-label text-stone-500 border-b border-stone-800">
											<th class="px-4 py-2.5 text-left">Name</th>
											<th class="px-4 py-2.5 text-left">Difficulty</th>
											<th class="px-4 py-2.5 text-left">Type</th>
											<th class="px-4 py-2.5 text-right">Points</th>
											<th class="px-4 py-2.5 text-right">Solves</th>
											<th class="px-4 py-2.5 text-left">Status</th>
											<th class="px-4 py-2.5 text-right">Actions</th>
										</tr>
									</thead>
									<tbody>
										{#each challenges as challenge}
											<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
												<td class="px-4 py-2.5">
													<a href="/challenges/{challenge.slug}" class="text-stone-200 hover:text-amber-400 transition-colors">{challenge.name}</a>
												</td>
												<td class="px-4 py-2.5">
													<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}"><span class="badge-label">{challenge.difficulty}</span></span>
												</td>
												<td class="px-4 py-2.5">
													<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
														<OpticalIcon icon={resourceIcon(challenge.resource_type)} size={12} box={12} /><span class="badge-label">{resourceLabel(challenge.resource_type)}</span>
													</span>
												</td>
												<td class="px-4 py-2.5 text-right text-stone-200 tabular-nums">{challenge.base_points}</td>
												<td class="px-4 py-2.5 text-right text-stone-400 tabular-nums">{challenge.total_solves || 0}</td>
												<td class="px-4 py-2.5">
													<span class="inline-flex items-center gap-1.5 text-xs leading-none {challenge.status === 'published' ? 'text-up' : 'text-warn'}">
														<span class="w-1.5 h-1.5 rounded-full {challenge.status === 'published' ? 'bg-up' : 'bg-warn'}"></span>
														<span class="optical-label">{challenge.status === 'published' ? 'Published' : 'Draft'}</span>
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

			{#if activeTab === 'users'}
				{#if users.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:account-off-outline" text="No users yet." />
					</Card>
				{:else}
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
									<span title={instantTitle(user.created_at, 'seconds')}>Joined {formatDate(user.created_at)}</span>
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
										on:click={() => toggleBan(user)}
										disabled={actionLoading === user.id}
										class="text-xs {user.is_banned ? 'text-up' : 'text-warn'} hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
										title={user.is_banned ? 'Unban user' : 'Ban user'}
									>
										{user.is_banned ? 'Unban' : 'Ban'}
									</button>
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

					<div class="hidden lg:block">
						<Card title="Users" bodyClass="">
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{users.length}</span>
							<div class="overflow-x-auto">
								<table class="w-full min-w-[680px] text-sm">
									<thead>
										<tr class="metadata-label text-stone-500 border-b border-stone-800">
											<th class="px-4 py-2.5 text-left">User</th>
											<th class="px-4 py-2.5 text-left hidden md:table-cell">Email</th>
											<th class="px-4 py-2.5 text-left">Role</th>
											<th class="px-4 py-2.5 text-right">Score</th>
											<th class="px-4 py-2.5 text-right hidden md:table-cell">Joined</th>
											<th class="px-4 py-2.5 text-right">Actions</th>
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
											<td class="px-4 py-2.5 text-right text-stone-500 tabular-nums hidden md:table-cell" title={instantTitle(user.created_at, 'seconds')}>{formatDate(user.created_at)}</td>
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
															on:click={() => toggleBan(user)}
															disabled={actionLoading === user.id}
															class="text-xs {user.is_banned ? 'text-up' : 'text-warn'} hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
															title={user.is_banned ? 'Unban user' : 'Ban user'}
														>
															{user.is_banned ? 'Unban' : 'Ban'}
														</button>
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

			{#if activeTab === 'teams'}
				<div class="flex items-center gap-2 mb-4">
					<input
						type="text"
						bind:value={teamsQuery}
						on:input={() => loadTeams()}
						placeholder="Search teams by name..."
						class="flex-1 text-sm bg-stone-950 border border-stone-800 rounded-md px-3 py-2 text-stone-200 focus:outline-none focus:border-stone-500"
					/>
					<select
						bind:value={teamsSort}
						on:change={() => loadTeams()}
						class="text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-2 text-stone-200 focus:outline-none focus:border-stone-500"
					>
						<option value="score">Sort: Score</option>
						<option value="created">Sort: Newest</option>
						<option value="name">Sort: Name</option>
					</select>
				</div>

				{#if teamsError}
					<Card hasHeader={false}><p class="text-sm text-down p-4">{teamsError}</p></Card>
				{:else if teamsLoading && teams.length === 0}
					<Card hasHeader={false}><p class="text-sm text-stone-500 p-4">Loading teams...</p></Card>
				{:else if teams.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:account-multiple-outline" text="No teams yet." />
					</Card>
				{:else}
					<Card title="Teams" bodyClass="">
						<span slot="meta" class="text-stone-500 text-xs tabular-nums">{teams.length}</span>
						<div class="overflow-x-auto">
							<table class="w-full min-w-[820px] text-sm">
								<thead>
									<tr class="metadata-label text-stone-500 border-b border-stone-800">
										<th class="px-4 py-2.5 text-left">Team</th>
										<th class="px-4 py-2.5 text-right">Members</th>
										<th class="px-4 py-2.5 text-right">Score</th>
										<th class="px-4 py-2.5 text-right hidden md:table-cell">Max</th>
										<th class="px-4 py-2.5 text-left hidden md:table-cell">Join code</th>
										<th class="px-4 py-2.5 text-right hidden lg:table-cell">Created</th>
										<th class="px-4 py-2.5 text-right">Actions</th>
									</tr>
								</thead>
								<tbody>
									{#each teams as team}
										<tr class="border-b border-stone-800/60 hover:bg-stone-800/20 transition-colors">
											<td class="px-4 py-2.5">
												<button class="text-stone-200 hover:underline text-left" on:click={() => renameTeam(team)} title="Rename team">{team.name}</button>
											</td>
											<td class="px-4 py-2.5 text-right tabular-nums">
												<button class="text-stone-300 hover:underline" on:click={() => toggleTeamExpand(team.id)} title="Show members">
													{team.member_count}{expandedTeams[team.id] ? ' ▾' : ' ▸'}
												</button>
											</td>
											<td class="px-4 py-2.5 text-right text-stone-200 tabular-nums">
												<button class="hover:underline" on:click={() => editTeamScore(team)} title="Edit score">{team.total_score ?? 0}</button>
											</td>
											<td class="px-4 py-2.5 text-right text-stone-400 tabular-nums hidden md:table-cell">
												<button class="hover:underline" on:click={() => editTeamMax(team)} title="Edit max members">{team.max_members == null ? '∞' : team.max_members}</button>
											</td>
											<td class="px-4 py-2.5 hidden md:table-cell">
												<span class="font-mono text-xs text-stone-400">{team.join_code}</span>
												<button class="text-xs text-stone-500 hover:underline ml-2 disabled:opacity-50" on:click={() => rotateTeamCode(team)} disabled={actionLoading === team.id} title="Rotate join code">rotate</button>
											</td>
											<td class="px-4 py-2.5 text-right text-stone-500 tabular-nums hidden lg:table-cell" title={team.created_at ? instantTitle(team.created_at, 'seconds') : ''}>{team.created_at ? formatDate(team.created_at) : '—'}</td>
											<td class="px-4 py-2.5">
												<div class="flex items-center justify-end gap-3">
													<button class="text-xs text-stone-400 hover:underline" on:click={() => toggleTeamExpand(team.id)}>Members</button>
													<button class="text-xs text-stone-400 hover:underline" on:click={() => toggleSolvesExpand(team.id)}>Solves</button>
													<button class="text-xs text-down hover:underline disabled:opacity-50 disabled:cursor-not-allowed" on:click={() => disbandTeam(team)} disabled={actionLoading === team.id} title="Disband team">
														{actionLoading === team.id ? '...' : 'Disband'}
													</button>
												</div>
											</td>
										</tr>
										{#if expandedTeams[team.id]}
											<tr class="border-b border-stone-800/60 bg-stone-900/30">
												<td class="px-4 py-3" colspan="7">
													<div class="space-y-2">
														{#if team.members && team.members.length}
															{#each team.members as member}
																<div class="flex items-center justify-between text-sm">
																	<span class="text-stone-300">{member.username}</span>
																	<button class="text-xs text-warn hover:underline disabled:opacity-50 disabled:cursor-not-allowed" on:click={() => kickMember(team, member)} disabled={actionLoading === team.id}>Kick</button>
																</div>
															{/each}
														{:else}
															<p class="text-xs text-stone-500">No members.</p>
														{/if}
														<div class="flex items-center gap-2 pt-2 border-t border-stone-800">
															<input
																type="text"
																bind:value={addMemberInput[team.id]}
																on:keydown={(e) => e.key === 'Enter' && addTeamMember(team)}
																placeholder="username to add / move..."
																class="flex-1 text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-1.5 text-stone-200 focus:outline-none focus:border-stone-500"
															/>
															<button class="text-xs text-up hover:underline disabled:opacity-50 disabled:cursor-not-allowed" on:click={() => addTeamMember(team)} disabled={actionLoading === team.id}>Add member</button>
														</div>
													</div>
												</td>
											</tr>
										{/if}
										{#if expandedSolves[team.id]}
											<tr class="border-b border-stone-800/60 bg-stone-900/30">
												<td class="px-4 py-3" colspan="7">
													{#if solvesLoading[team.id]}
														<p class="text-xs text-stone-500">Loading solves...</p>
													{:else if teamSolves[team.id] && teamSolves[team.id].length}
														<div class="overflow-x-auto">
															<table class="w-full min-w-[420px] text-xs">
																<thead>
																	<tr class="metadata-label text-stone-500 border-b border-stone-800">
																		<th class="px-2 py-1.5 text-left">Challenge</th>
																		<th class="px-2 py-1.5 text-left">Solver</th>
																		<th class="px-2 py-1.5 text-right">Points</th>
																		<th class="px-2 py-1.5 text-right">When</th>
																	</tr>
																</thead>
																<tbody>
																	{#each teamSolves[team.id] as solve}
																		<tr class="border-b border-stone-800/40">
																			<td class="px-2 py-1.5 text-stone-300">{solve.challenge_name}{solve.flag_name ? ` · ${solve.flag_name}` : ''}</td>
																			<td class="px-2 py-1.5 text-stone-400">{solve.solver_username}</td>
																			<td class="px-2 py-1.5 text-right text-stone-300 tabular-nums">{solve.points}</td>
																			<td class="px-2 py-1.5 text-right text-stone-500 tabular-nums" title={instantTitle(solve.solved_at, 'seconds')}>{formatDate(solve.solved_at)}</td>
																		</tr>
																	{/each}
																</tbody>
															</table>
														</div>
													{:else}
														<p class="text-xs text-stone-500">No solves yet.</p>
													{/if}
												</td>
											</tr>
										{/if}
									{/each}
								</tbody>
							</table>
						</div>
					</Card>
				{/if}
			{/if}

			{#if activeTab === 'infrastructure'}
				{#if infrastructureError}
					<div class="mb-6 flex items-center justify-between gap-3 rounded-lg border border-warn/20 bg-warn/5 px-4 py-3 text-sm text-warn" aria-live="polite">
						<span>Infrastructure data could not be loaded: {infrastructureError}</span>
						<button type="button" on:click={loadDashboard} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
					</div>
				{/if}
				<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">Nodes</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.nodes?.online || 0}/{infraStats?.nodes?.total || 0}
						</p>
						<p class="text-xs text-up mt-1">online</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">vCPU</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.vcpu?.used || 0}/{infraStats?.resources?.vcpu?.total || 0}
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.vcpu?.available || 0} available</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">Memory</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.memory_gb?.used || 0}/{infraStats?.resources?.memory_gb?.total || 0} GB
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.memory_gb?.available || 0} GB free</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">Running VMs</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">{infraStats?.vms?.running || 0}</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">of {infraStats?.vms?.total || 0} total</p>
					</div>
				</div>

				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
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
												<span class="optical-label text-sm text-stone-200 font-medium truncate">{node.name}</span>
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
										<div class="mt-2 space-y-1">
											<div class="flex items-center gap-2">
												<span class="metadata-label text-stone-600 w-10">CPU</span>
												<div class="flex-1 h-1.5 bg-stone-800 rounded-full overflow-hidden">
													<div
														class="h-full bg-stone-400 transition-all"
														style="width: {node.total_vcpu ? (node.used_vcpu / node.total_vcpu * 100) : 0}%"
													></div>
												</div>
											</div>
											<div class="flex items-center gap-2">
												<span class="metadata-label text-stone-600 w-10">RAM</span>
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

				<div class="mb-8">
					<Card title="Active VM Instances" bodyClass="">
						<span slot="meta" class="text-stone-500 text-xs tabular-nums">{activeInstances.length} running</span>
						{#if activeInstances.length === 0}
							<EmptyState icon="mdi:desktop-mac-dashboard" text="No active VM instances." />
						{:else}
							<div class="overflow-x-auto">
								<table class="w-full min-w-[640px] text-sm">
									<thead>
										<tr class="metadata-label text-stone-500 border-b border-stone-800">
											<th class="px-4 py-2.5 text-left">User</th>
											<th class="px-4 py-2.5 text-left">Challenge</th>
											<th class="px-4 py-2.5 text-left">IP</th>
											<th class="px-4 py-2.5 text-left">Status</th>
											<th class="px-4 py-2.5 text-right">Expires</th>
											<th class="px-4 py-2.5 text-right">Actions</th>
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
													{instance.expires_at
														? formatLocalDateTimeWithZone(instance.expires_at, 'seconds')
														: '—'}
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

				<Card title="Active Docker Instances" bodyClass="">
					<span slot="meta" class="text-stone-500 text-xs tabular-nums">{activeDockerInstances.length} running</span>
					{#if activeDockerInstances.length === 0}
						<EmptyState icon="mdi:docker" text="No active Docker instances." />
					{:else}
						<div class="overflow-x-auto">
							<table class="w-full min-w-[640px] text-sm">
								<thead>
									<tr class="metadata-label text-stone-500 border-b border-stone-800">
										<th class="px-4 py-2.5 text-left">User</th>
										<th class="px-4 py-2.5 text-left">Challenge</th>
										<th class="px-4 py-2.5 text-left">IP</th>
										<th class="px-4 py-2.5 text-left">Status</th>
										<th class="px-4 py-2.5 text-right">Expires</th>
										<th class="px-4 py-2.5 text-right">Actions</th>
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
												{instance.expires_at
													? formatLocalDateTimeWithZone(instance.expires_at, 'seconds')
													: '—'}
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

			{#if activeTab === 'settings'}
				{#if settingsError}
					<div class="mb-6 flex items-center justify-between gap-3 rounded-lg border border-down/20 bg-down/10 px-4 py-3 text-sm text-down" aria-live="polite">
						<span>Settings could not be loaded: {settingsError}</span>
						<button type="button" on:click={loadDashboard} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
					</div>
				{/if}
				<div class="space-y-6 {settingsError ? 'pointer-events-none select-none opacity-40' : ''}" aria-disabled={settingsError ? 'true' : undefined}>
					{#if settingsChanged}
						<div class="flex justify-end">
							<button on:click={savePlatformSettings} disabled={savingSettings || !!eventWindowError} class={btnPrimary}>
								{#if savingSettings}
									<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
								{:else}
									<Icon icon="mdi:content-save-outline" class="w-3.5 h-3.5 shrink-0" />
								{/if}
								Save Settings
							</button>
						</div>
					{/if}

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:timer-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Instance Timeouts</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Defaults applied when a new VM challenge is created</p>
						</div>
						<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
							{#each [
								{ key: 'vm_default_timeout_easy', label: 'Easy', default: 60 },
								{ key: 'vm_default_timeout_medium', label: 'Medium', default: 120 },
								{ key: 'vm_default_timeout_hard', label: 'Hard', default: 180 },
								{ key: 'vm_default_timeout_insane', label: 'Insane', default: 240 }
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

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:timer-sand" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Cooldown Periods</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Defaults applied when a new challenge is created</p>
						</div>
						<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
							{#each [
								{ key: 'cooldown.easy_minutes', label: 'Easy', default: 5 },
								{ key: 'cooldown.medium_minutes', label: 'Medium', default: 10 },
								{ key: 'cooldown.hard_minutes', label: 'Hard', default: 15 },
								{ key: 'cooldown.insane_minutes', label: 'Insane', default: 20 }
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

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:clock-plus-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Extension Settings</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Default extension count for new challenges; duration applies to every extension</p>
						</div>
						<div class="grid grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>Default Max Extensions</span>
								<input
									type="number"
							value={platformSettings['instance.max_extensions'] ?? 3}
							on:input={(e) => handleNumberInput(e, 'instance.max_extensions')}
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
								value={platformSettings['instance.extension_minutes'] ?? 30}
								on:input={(e) => handleNumberInput(e, 'instance.extension_minutes')}
										min="15"
										max="120"
										class="w-full {fieldCls} tabular-nums"
									/>
									<span class="text-xs text-stone-500">min</span>
								</div>
							</label>
						</div>
					</Card>

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:account-multiple-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">User Limits</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Resource limits per user</p>
						</div>
					<div class="max-w-sm">
						<label class="block">
							<span class={labelCls}>Max Concurrent Instances</span>
							<input
								type="number"
								value={platformSettings['instance.max_per_user'] ?? 2}
								on:input={(e) => handleNumberInput(e, 'instance.max_per_user')}
								min="1"
								max="5"
								class="w-full {fieldCls} tabular-nums"
							/>
						</label>
					</div>
					</Card>

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:vpn" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">VPN Settings</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Instance access policy; server keys and endpoint remain deployment configuration</p>
						</div>
						<div class="max-w-sm">
							<label class="block">
								<span class={labelCls}>Require VPN for Instances</span>
								<select
									value={String(platformSettings['platform.require_vpn'] ?? true)}
									on:change={(e) => handleSelectChange(e, 'platform.require_vpn')}
									class="w-full {fieldCls}"
								>
									<option value="true">Yes</option>
									<option value="false">No</option>
								</select>
							</label>
						</div>
					</Card>

					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:cog-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Platform Settings</span>
							</h2>
							<p class="text-xs text-stone-500 mt-1 normal-case font-normal tracking-normal">Access controls and the public event clock</p>
						</div>
						<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
							<label class="block">
								<span class={labelCls}>Registration</span>
								<select
									value={platformSettings.registration_mode ?? 'open'}
									on:change={(e) => handleTextInput(e, 'registration_mode')}
									class="w-full {fieldCls}"
								>
									<option value="open">Open</option>
									<!-- invite-only removed: no invite-code mint UI, and registration is at ZeroPool -->
									<option value="disabled">Closed</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Scoreboard</span>
								<select
									value={String(platformSettings.scoreboard_enabled ?? true)}
									on:change={(e) => handleSelectChange(e, 'scoreboard_enabled')}
									class="w-full {fieldCls}"
								>
									<option value="true">Public</option>
									<option value="false">Hidden</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Arena (A/D · KotH)</span>
								<select
									value={String(platformSettings.arena_enabled ?? false)}
									on:change={(e) => handleSelectChange(e, 'arena_enabled')}
									class="w-full {fieldCls}"
								>
									<option value="true">Enabled</option>
									<option value="false">Disabled</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Teams</span>
								<select
									value={String(platformSettings.teams_mode ?? false)}
									on:change={(e) => handleSelectChange(e, 'teams_mode')}
									class="w-full {fieldCls}"
								>
									<option value="true">Team-based</option>
									<option value="false">Individual</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Economy</span>
								<select
									value={String(platformSettings.economy_mode ?? false)}
									on:change={(e) => handleSelectChange(e, 'economy_mode')}
									class="w-full {fieldCls}"
								>
									<option value="true">Enabled (credits + dynamic scoring)</option>
									<option value="false">Off (standard scoring)</option>
								</select>
							</label>
							<div class="md:col-span-2 mt-1 border-t border-stone-800/70 pt-4">
								<div class="mb-3 flex items-start justify-between gap-3">
									<div>
										<h3 class="text-sm font-medium text-stone-300">CTF window</h3>
										<p class="mt-1 text-xs text-stone-500">Shown in {browserTimeZone}; saved as timezone-safe UTC instants.</p>
									</div>
									{#if platformSettings['event.start_at'] || platformSettings['event.end_at']}
										<button
											type="button"
											on:click={() => {
												updateSetting('event.start_at', '');
												updateSetting('event.end_at', '');
											}}
											class="shrink-0 text-xs text-stone-500 transition-colors hover:text-stone-300"
										>Clear</button>
									{/if}
								</div>
								<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
									<label class="block">
										<span class={labelCls}>Starts</span>
										<input
											type="datetime-local"
											step="60"
											value={datetimeLocalValue(platformSettings['event.start_at'])}
											on:input={(e) => handleEventTimeInput(e, 'event.start_at')}
											class="w-full {fieldCls} tabular-nums"
										/>
									</label>
									<label class="block">
										<span class={labelCls}>Ends</span>
										<input
											type="datetime-local"
											step="60"
											value={datetimeLocalValue(platformSettings['event.end_at'])}
											on:input={(e) => handleEventTimeInput(e, 'event.end_at')}
											class="w-full {fieldCls} tabular-nums"
										/>
									</label>
								</div>
								{#if eventWindowError}
									<p class="mt-2 text-xs text-down" aria-live="polite">{eventWindowError}</p>
								{/if}
							</div>
						</div>
					</Card>
				</div>
			{:else if activeTab === 'intel'}
				<div class="space-y-8">
					{#if intelError}
						<div class="flex items-center justify-between gap-3 rounded-lg border border-down/20 bg-down/10 px-4 py-3 text-sm text-down" aria-live="polite">
							<span>Audit data could not be loaded: {intelError}</span>
							<button type="button" on:click={loadIntel} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
						</div>
					{/if}
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
											<tr class="metadata-label text-stone-500 border-b border-stone-800">
												<th class="px-4 py-2.5 text-left">Challenge</th>
												<th class="px-4 py-2.5 text-left">Flag Owner</th>
												<th class="px-4 py-2.5 text-left">Submitter</th>
												<th class="px-4 py-2.5 text-left">IP</th>
												<th class="px-4 py-2.5 text-left">Flag Value</th>
												<th class="px-4 py-2.5 text-right">Time</th>
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
												<td
													class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums"
													title={instantTitle(ev.created_at, 'seconds')}
												>{formatLocalDateTimeWithZone(ev.created_at, 'seconds')}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							</Card>
						{/if}
					</div>

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
											<tr class="metadata-label text-stone-500 border-b border-stone-800">
												<th class="px-4 py-2.5 text-left">User</th>
												<th class="px-4 py-2.5 text-left">Challenge</th>
												<th class="px-4 py-2.5 text-left">Flag Value</th>
												<th class="px-4 py-2.5 text-left">Instance ID</th>
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

{#if showCreateModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showCreateModal = false}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-2xl max-h-[90vh] flex flex-col" role="dialog" aria-modal="true">
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
						on:click={() => newChallenge.type = 'download'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'download' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:file-download-outline" class="w-3.5 h-3.5 shrink-0" />
						Download only
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
							<select value={newChallenge.difficulty} on:change={changeChallengeDifficulty} class="w-full {fieldCls}">
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

					{#if newChallenge.type === 'container' || newChallenge.type === 'download'}
						<div class="pt-4 border-t border-stone-800 space-y-5">
							{#if newChallenge.type === 'download'}
								<div class="flex items-start gap-2 py-2.5 px-3 bg-stone-900/40 border border-stone-800 rounded-md text-stone-400 text-xs">
									<Icon icon="mdi:information-outline" class="w-4 h-4 shrink-0 mt-0.5" />
									A download-only challenge has no container — add the challenge files as attachments below, and a flag.
								</div>
							{/if}
							{#if newChallenge.type === 'container'}
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

							<label class="block">
								<span class={labelCls}>Platform</span>
								<select bind:value={newChallenge.container_platform} class="w-full {fieldCls}">
									<option value="">Auto (native architecture)</option>
									<option value="linux/amd64">linux/amd64</option>
									<option value="linux/arm64">linux/arm64</option>
								</select>
								<p class="text-stone-500 text-xs mt-2">Set if the image architecture differs from the server (e.g. amd64 image on ARM host)</p>
							</label>

							<div>
								<div class="flex items-center justify-between mb-2">
									<span class="metadata-label block text-stone-400">Exposed Ports</span>
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
							{/if}

							<div>
								<div class="flex items-center justify-between mb-2">
									<span class="metadata-label block text-stone-400">Flags</span>
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

							{#if newChallenge.type === 'container'}
							<div>
								<span class="metadata-label block text-stone-400 mb-3">Instance Settings</span>
								<div class="grid grid-cols-3 gap-3">
									<label class="block">
										<span class="metadata-label block text-stone-500 mb-1">Timeout (min)</span>
										<input type="number" bind:value={newChallenge.instance_timeout} min="1" class="w-full {fieldCls} tabular-nums" />
									</label>
									<label class="block">
										<span class="metadata-label block text-stone-500 mb-1">Max Extensions</span>
										<input type="number" bind:value={newChallenge.max_extensions} min="0" class="w-full {fieldCls} tabular-nums" />
									</label>
									<label class="block">
										<span class="metadata-label block text-stone-500 mb-1">Cooldown (min)</span>
										<input type="number" bind:value={newChallenge.cooldown_minutes} min="0" class="w-full {fieldCls} tabular-nums" />
									</label>
								</div>
								<p class="text-stone-500 text-xs mt-1.5">How long the instance runs, how many extensions, and cooldown between resets</p>
							</div>
							{/if}
						</div>
					{:else if newChallenge.type === 'ova'}
						<div class="pt-4 border-t border-stone-800 space-y-5">
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
								</div>
								<p class="text-stone-500 text-xs mt-2">Upload OVA/qcow2/vmdk images under Infrastructure → Templates; they're converted there, then selected here.</p>
							</div>

							{#if newChallenge.vm_source === 'template'}
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

							<div>
								<div class="flex items-center justify-between mb-3">
									<span class="metadata-label block text-stone-400">Flags <span class="font-mono tracking-normal tabular-nums">({newChallenge.flags.length})</span></span>
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

					<div class="pt-4 border-t border-stone-800 space-y-3">
						<div class="flex items-center justify-between">
							<span class="metadata-label block text-stone-400">File Attachments <span class="text-stone-500 font-normal">(optional)</span></span>
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

			<div class="px-6 border-b border-stone-800 flex gap-1 bg-stone-950">
				{#each [{ id: 'settings', label: 'Settings', n: 0 }, { id: 'flags', label: 'Flags', n: editFlags.length }, { id: 'hints', label: 'Hints', n: editHints.length }, { id: 'files', label: 'Files', n: editAttachments.length }] as t}
					<button type="button" on:click={() => (editTab = t.id as typeof editTab)} class="px-3.5 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors {editTab === t.id ? 'border-amber-500 text-stone-100' : 'border-transparent text-stone-500 hover:text-stone-300'}">
						{t.label}{#if t.n}<span class="ml-1.5 text-xs tabular-nums {editTab === t.id ? 'text-amber-500' : 'text-stone-600'}">{t.n}</span>{/if}
					</button>
				{/each}
			</div>

			{#if subError}<div class="mx-6 mt-4 px-3 py-2 rounded-md bg-down/10 border border-down/30 text-down text-xs">{subError}</div>{/if}
			{#if subNote}<div class="mx-6 mt-4 px-3 py-2 rounded-md bg-up/10 border border-up/30 text-up text-xs">{subNote}</div>{/if}

			{#if editTab === 'settings'}
			<form on:submit|preventDefault={handleEditChallenge} class="p-6 space-y-5">

				<label class="block">
					<span class={labelCls}>Name *</span>
					<input type="text" bind:value={editingChallenge.name} required class="w-full {fieldCls}" />
				</label>

				<label class="block">
					<span class={labelCls}>Description</span>
					<textarea bind:value={editingChallenge.description} rows="4" class="w-full {fieldCls} resize-none"></textarea>
				</label>

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

				{#if editingChallenge.resource_type !== 'vm'}
					<div class="border border-stone-800 rounded-lg p-4 space-y-4">
						<h3 class="metadata-label text-stone-400">Container Settings</h3>

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
								<input type="text" bind:value={editingChallenge.memory_limit} placeholder="512Mi" class="w-full {fieldCls}" />
							</label>
						</div>

						<div>
							<div class="flex items-center justify-between mb-2">
								<span class="metadata-label block text-stone-400">Exposed Ports</span>
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

				{#if editingChallenge.resource_type === 'vm'}
					<div class="border border-stone-800 rounded-lg p-4">
						<h3 class="metadata-label text-stone-400 mb-3">VM Settings</h3>
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

				<div class="border border-stone-800 rounded-lg p-4 space-y-3">
					<h3 class="metadata-label text-stone-400">Instance Settings</h3>
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
			{/if}

			{#if editTab === 'flags'}
				<div class="p-6 space-y-3">
					{#if subLoading}
						<p class="text-stone-500 text-sm">Loading…</p>
					{:else}
						{#each editFlags as f, i (i)}
							<div class="border border-stone-800 rounded-lg p-3 space-y-2">
								<div class="flex items-center gap-2">
									<input type="text" bind:value={f.name} placeholder="Flag name" class="flex-1 {fieldCls}" />
									<input type="number" bind:value={f.points} min="1" placeholder="pts" title="Points" class="w-20 {fieldCls} tabular-nums" />
									<button type="button" on:click={() => saveEditFlag(f, i)} disabled={savingSub === 'flag' + i} class="{btnPrimary} px-3">{savingSub === 'flag' + i ? '…' : 'Save'}</button>
									<button type="button" on:click={() => deleteEditFlag(f, i)} title="Delete flag" class="p-1.5 text-stone-600 hover:text-down transition-colors"><Icon icon="mdi:trash-can-outline" class="w-4 h-4" /></button>
								</div>
								<div class="flex items-center gap-2">
									<select bind:value={f.flag_type} class="w-32 {fieldCls}">
										<option value="static">Static</option>
										<option value="regex">Regex</option>
										<option value="dynamic">Dynamic</option>
									</select>
									{#if f.flag_type === 'dynamic'}
										<input type="text" bind:value={f.dynamic_flag_prefix} placeholder="Prefix e.g. H7CTF" class="flex-1 font-mono {fieldCls}" />
									{:else}
										<input type="text" bind:value={f.flag} placeholder={f.has_value ? '•••••••• (unchanged — type to replace)' : (f.flag_type === 'regex' ? 'regex pattern' : 'flag value')} class="flex-1 font-mono {fieldCls}" />
									{/if}
								</div>
							</div>
						{/each}
						{#if !editFlags.length}<p class="text-stone-600 text-sm">No flags yet.</p>{/if}
						<button type="button" on:click={addEditFlag} class="text-sm text-stone-400 hover:text-stone-200 transition-colors flex items-center gap-1.5"><Icon icon="mdi:plus" class="w-4 h-4" /> Add Flag</button>
					{/if}
				</div>
			{/if}

			{#if editTab === 'hints'}
				<div class="p-6 space-y-3">
					{#if subLoading}
						<p class="text-stone-500 text-sm">Loading…</p>
					{:else}
						{#each editHints as hnt, i (i)}
							<div class="border border-stone-800 rounded-lg p-3 space-y-2">
								<textarea bind:value={hnt.content} rows="2" placeholder="Hint text — shown to players who unlock it" class="w-full {fieldCls} resize-none"></textarea>
								<div class="flex items-center gap-2">
									<label class="text-xs text-stone-500 flex items-center gap-1.5">Cost <input type="number" bind:value={hnt.cost} min="0" title="Point cost to unlock" class="w-20 {fieldCls} tabular-nums" /></label>
									<span class="flex-1"></span>
									<button type="button" on:click={() => saveEditHint(hnt, i)} disabled={savingSub === 'hint' + i} class="{btnPrimary} px-3">{savingSub === 'hint' + i ? '…' : 'Save'}</button>
									<button type="button" on:click={() => deleteEditHint(hnt, i)} title="Delete hint" class="p-1.5 text-stone-600 hover:text-down transition-colors"><Icon icon="mdi:trash-can-outline" class="w-4 h-4" /></button>
								</div>
							</div>
						{/each}
						{#if !editHints.length}<p class="text-stone-600 text-sm">No hints yet.</p>{/if}
						<button type="button" on:click={addEditHint} class="text-sm text-stone-400 hover:text-stone-200 transition-colors flex items-center gap-1.5"><Icon icon="mdi:plus" class="w-4 h-4" /> Add Hint</button>
					{/if}
				</div>
			{/if}

			{#if editTab === 'files'}
				<div class="p-6 space-y-3">
					{#if subLoading}
						<p class="text-stone-500 text-sm">Loading…</p>
					{:else}
						{#each editAttachments as a (a.id)}
							<div class="border border-stone-800 rounded-lg p-3 flex items-center gap-3">
								<Icon icon="mdi:file-outline" class="w-4 h-4 text-stone-500 shrink-0" />
								<span class="flex-1 text-sm text-stone-200 truncate">{a.filename}</span>
								<span class="text-xs text-stone-500 tabular-nums">{humanSize(a.file_size)}</span>
								<button type="button" on:click={() => deleteEditAttachment(a)} title="Delete file" class="p-1.5 text-stone-600 hover:text-down transition-colors"><Icon icon="mdi:trash-can-outline" class="w-4 h-4" /></button>
							</div>
						{/each}
						{#if !editAttachments.length}<p class="text-stone-600 text-sm">No files attached.</p>{/if}
						<label class="inline-flex items-center gap-1.5 text-sm text-stone-400 hover:text-stone-200 transition-colors cursor-pointer">
							<Icon icon="mdi:upload" class="w-4 h-4" /> {subUploading ? 'Uploading…' : 'Upload file'}
							<input type="file" multiple on:change={uploadEditAttachment} disabled={subUploading} class="hidden" />
						</label>
					{/if}
				</div>
			{/if}
		</div>
	</div>
{/if}

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
