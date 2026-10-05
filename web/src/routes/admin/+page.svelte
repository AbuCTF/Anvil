<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, type AdminAnnouncement, type EconomyPolicyDocument, type GradedAdminInfo, type RegistryImageInspection } from '$api';
	import Icon from '@iconify/svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import SecurityIntelligence from '$lib/components/admin/SecurityIntelligence.svelte';
	import { difficultyClass, resourceClass, resourceIcon, resourceLabel } from '$lib/rank';
	import { formatLocalDateLong, formatLocalDateTime, formatLocalDateTimeWithZone, instantTitle, viewerTimeZone } from '$lib/time';
	import { confirmDialog, alertDialog, promptDialog } from '$lib/stores/dialog';
	import { platformInfo, refreshPlatformInfo } from '$lib/stores/platform';
	import EventSetup from '$lib/components/admin/EventSetup.svelte';
	import DataWorkspace from '$lib/components/admin/DataWorkspace.svelte';
	import TeamDossier from '$lib/components/admin/TeamDossier.svelte';
	import UserDossier from '$lib/components/admin/UserDossier.svelte';
	import ChallengeDossier from '$lib/components/admin/ChallengeDossier.svelte';
	import MailWorkspace from '$lib/components/admin/MailWorkspace.svelte';
	import RegistryCredentials from '$lib/components/admin/RegistryCredentials.svelte';
	import RegistryBulkImport from '$lib/components/admin/RegistryBulkImport.svelte';

	let activeTab = 'overview';
	let loading = true;
	let stats: any = null;
	let users: any[] = [];
	let challenges: any[] = [];
	let error = '';
	let categoriesError = '';
	let showCreateModal = false;
	let showParticipantModal = false;
	let showRegistryImportModal = false;
	let showEditModal = false;
	let editingChallenge: any = null;
	let actionLoading = '';
	let selectedUserDetail: any = null;
	let selectedUserSeed: any = null;
	let userDetailLoading = false;
	let userDetailError = '';
	let userDossierAction = '';
	let selectedTeamDetail: any = null;
	let selectedTeamSeed: any = null;
	let teamDetailLoading = false;
	let teamDetailError = '';
	let teamCreditAdjusting = false;
	let teamDossierAction = '';
	let selectedChallengeDetail: any = null;
	let selectedChallengeSeed: any = null;
	let challengeDetailLoading = false;
	let challengeDetailError = '';
	let participantBusy = false;
	let participantError = '';
	let dataImportEntity = 'categories';
	let participantForm = {
		username: '', email: '', display_name: '', role: 'user', provisioning: 'activation_email'
	};

	let infraStats: any = null;
	let nodes: any[] = [];
	let templates: any[] = [];
	let activeInstances: any[] = [];
	let activeDockerInstances: any[] = [];
	let infrastructureError = '';
	let infrastructureRefreshing = false;
	let infrastructureUpdatedAt: Date | null = null;
	let infrastructureTimer: ReturnType<typeof setInterval> | undefined;

	let teams: any[] = [];
	let teamsLoading = false;
	let teamsError = '';
	let teamsQuery = '';
	let teamsSort = 'score';
	let usersQuery = '';
	let usersPage = 1;
	let teamsPage = 1;
	const adminPageSize = 50;
	$: normalizedUsersQuery = usersQuery.trim().toLowerCase();
	$: filteredUsers = normalizedUsersQuery
		? users.filter((user) => [user.username, user.email, user.role].some((value) => String(value || '').toLowerCase().includes(normalizedUsersQuery)))
		: users;
	$: usersPageCount = Math.max(1, Math.ceil(filteredUsers.length / adminPageSize));
	$: if (usersPage > usersPageCount) usersPage = usersPageCount;
	$: pagedUsers = filteredUsers.slice((usersPage - 1) * adminPageSize, usersPage * adminPageSize);
	$: teamsPageCount = Math.max(1, Math.ceil(teams.length / adminPageSize));
	$: if (teamsPage > teamsPageCount) teamsPage = teamsPageCount;
	$: pagedTeams = teams.slice((teamsPage - 1) * adminPageSize, teamsPage * adminPageSize);

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
	let economyPolicyDocument: EconomyPolicyDocument | null = null;
	let economyPolicyLoading = false;
	let showEconomyPolicy = false;

	let announcements: AdminAnnouncement[] = [];
	let announcementsLoading = false;
	let announcementsError = '';
	let announcementSaving = false;
	let announcementForm = {
		title: '', body: '', severity: 'info' as AdminAnnouncement['severity'],
		audience: 'all' as AdminAnnouncement['audience'], href: '',
		publish_at: '', expires_at: '', pinned: false
	};

	let categories: any[] = [];
	let newChallenge = {
		name: '',
		description: '',
		sub_description: '',
		author_name: '',
		category: '',
		category_id: '',
		newCategoryName: '',
		difficulty: 'easy',
		base_points: 100,
		score_type: 'static',
		score_minimum: 100,
		score_decay: 50,
		flag: '',
		flags: [{ name: 'User Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }, { name: 'Root Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }],
		type: 'container',
		scoring_mode: 'flag',
		arena_mode: 'per_team',
		privesc: false,
		docker_image: '',
		container_platform: '',
		exposed_ports: [{ port: 1337, protocol: 'tcp', service: 'tcp' }],
		services: [] as any[],
		ova_url: '',
		vm_template_id: '',
		vm_source: 'template',
		vm_vcpu: 1,
		vm_memory_mb: 1024,
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
	let imageInspection: RegistryImageInspection | null = null;
	let imageInspectionError = '';
	let imageInspecting = false;
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
		if (selectedUserSeed) { selectedUserSeed = null; selectedUserDetail = null; }
		else if (selectedTeamSeed) { selectedTeamSeed = null; selectedTeamDetail = null; }
		else if (selectedChallengeSeed) { selectedChallengeSeed = null; selectedChallengeDetail = null; }
		else if (showParticipantModal) showParticipantModal = false;
		else if (showCreateModal) showCreateModal = false;
		else if (showEditModal) { showEditModal = false; editingChallenge = null; }
		else if (showNodeModal) showNodeModal = false;
		else if (showTemplateUploadModal) showTemplateUploadModal = false;
	}

	function openParticipantModal() {
		participantForm = { username: '', email: '', display_name: '', role: 'user', provisioning: 'activation_email' };
		participantError = '';
		showParticipantModal = true;
	}

	async function createParticipant() {
		if (participantBusy) return;
		participantBusy = true;
		participantError = '';
		try {
			const created = await api.createAdminUser(participantForm);
			showParticipantModal = false;
			await loadDashboard();
			await alertDialog({
				title: 'Participant created',
				message: created.emails_queued ? `${created.username} was created and their access email was queued.` : `${created.username} was created for SSO access.`
			});
		} catch (e) {
			participantError = e instanceof Error ? e.message : 'Could not create the participant.';
		} finally {
			participantBusy = false;
		}
	}

	function openPeopleImport() {
		dataImportEntity = 'users';
		setTab('data');
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

	function blankService(index = 0) {
		return {
			name: index === 0 ? 'app' : `service-${index + 1}`,
			image: '', tag: 'latest', command_text: '', env_text: '', public: index === 0,
			egress: false, cpu_limit: '1', memory_limit: '512Mi',
			ports: [{ port: index === 0 ? 8080 : 5432, protocol: 'tcp', service: index === 0 ? 'http' : 'tcp', internal: index !== 0 }]
		};
	}

	function addService() {
		newChallenge.services = [...newChallenge.services, blankService(newChallenge.services.length)];
	}

	function removeService(index: number) {
		newChallenge.services = newChallenge.services.filter((_, i) => i !== index);
	}

	function servicePayload(service: any) {
		const env = Object.fromEntries((service.env_text || '').split('\n').map((line: string) => line.trim()).filter(Boolean).map((line: string) => {
			const separator = line.indexOf('=');
			return separator < 1 ? [line, ''] : [line.slice(0, separator).trim(), line.slice(separator + 1)];
		}));
		return {
			name: service.name.trim(), image: service.image.trim(), tag: service.tag.trim() || 'latest',
			command: (service.command_text || '').split('\n').map((line: string) => line.trim()).filter(Boolean),
			public: !!service.public, egress: !!service.egress, env,
			cpu_limit: service.cpu_limit || '1', memory_limit: service.memory_limit || '512Mi',
			ports: (service.ports || []).filter((port: any) => Number(port.port) > 0).map((port: any) => ({
				port: Number(port.port), protocol: port.protocol || 'tcp', service: port.service || 'tcp', internal: !!port.internal
			}))
		};
	}

	function deliveryLabel(challenge: any) {
		if (challenge.delivery_type === 'multi') return 'Multi-service';
		if (challenge.delivery_type === 'static') return 'Files';
		if (challenge.delivery_type === 'external') return 'External target';
		return resourceLabel(challenge.resource_type);
	}

	function scoringModel(challenge: any) {
		if (challenge?.scoring_mode === 'graded') return 'graded';
		return challenge?.score_type === 'dynamic' ? 'dynamic' : 'static';
	}

	function setNewScoringModel(value: string) {
		newChallenge = {
			...newChallenge,
			scoring_mode: value === 'graded' ? 'graded' : 'flag',
			score_type: value === 'dynamic' ? 'dynamic' : 'static'
		};
	}

	function setEditScoringModel(value: string) {
		if (!editingChallenge) return;
		editingChallenge = {
			...editingChallenge,
			scoring_mode: value === 'graded' ? 'graded' : 'flag',
			score_type: value === 'dynamic' ? 'dynamic' : 'static'
		};
	}

	function deliveryIcon(challenge: any) {
		if (challenge.delivery_type === 'multi') return 'mdi:server-network';
		if (challenge.delivery_type === 'static') return 'mdi:file-download-outline';
		if (challenge.delivery_type === 'external') return 'mdi:open-in-new';
		return resourceIcon(challenge.resource_type);
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
			scoring_mode: challenge.scoring_mode || 'flag',
			score_type: challenge.score_type || 'static',
			score_minimum: challenge.score_minimum ?? challenge.base_points ?? 100,
			score_decay: challenge.score_decay ?? 50,
			delivery_type: challenge.delivery_type || (challenge.resource_type === 'vm' ? 'vm' : challenge.container_image ? 'container' : 'static'),
			sub_description: challenge.sub_description || '',
			arena_mode: challenge.arena_mode || 'per_team',
			privesc: !!challenge.privesc,
			services: (challenge.services || []).map((service: any) => ({
				...service,
				command_text: (service.command || []).join('\n'),
				env_text: Object.entries(service.env || {}).map(([key, value]) => `${key}=${value}`).join('\n'),
				ports: service.ports || []
			}))
		};
		editTab = 'settings';
		editFlags = []; editHints = []; editAttachments = [];
		grading = null; gradingError = ''; secretShown = false;
		subError = ''; subNote = '';
		showEditModal = true;
		void loadEditSubdata(challenge.id);
	}

	// --- edit modal: flags / hints / files management (CTFd-parity, inline CRUD) ---
	let editTab: 'settings' | 'flags' | 'hints' | 'files' | 'grading' = 'settings';
	let editFlags: any[] = [];
	let editHints: any[] = [];
	let editAttachments: any[] = [];
	let externalHandout = { name: '', url: '', sha256: '', description: '' };
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

	// --- edit modal: graded challenges (grader secret + recent evaluations) ---
	let grading: GradedAdminInfo | null = null;
	let gradingError = '';
	let gradingLoading = false;
	let secretShown = false;
	let secretCopied = false;
	const secretRef = '${GRADER_SECRET}';
	const graderEnv = `GRADER_SECRET: ${secretRef}\nGRADER_URL: \${GRADER_URL}\nANVIL_TEAM_ID: \${ANVIL_TEAM_ID}\nINSTANCE_ID: \${INSTANCE_ID}`;

	async function loadGrading() {
		if (!editingChallenge) return;
		gradingLoading = true; gradingError = '';
		try {
			grading = await api.getGradedAdmin(editingChallenge.id);
		} catch (e) {
			gradingError = e instanceof Error ? e.message : 'Failed to load grading';
		} finally {
			gradingLoading = false;
		}
	}

	async function copySecret() {
		if (!grading) return;
		try {
			await navigator.clipboard.writeText(grading.secret);
			secretCopied = true;
			setTimeout(() => (secretCopied = false), 1500);
		} catch {
			gradingError = 'Copy failed — reveal the secret and copy it by hand';
		}
	}

	async function rotateSecret() {
		if (!editingChallenge || !grading) return;
		if (!(await confirmDialog({
			title: 'Rotate grader secret',
			message: 'Running instances keep the old secret in their env, so their grader calls fail until the instances are restarted.',
			confirmLabel: 'Rotate',
			danger: true
		}))) return;
		try {
			const r = await api.rotateGradedSecret(editingChallenge.id);
			grading = { ...grading, secret: r.secret };
			secretShown = true;
			flash('Secret rotated');
		} catch (e) {
			gradingError = e instanceof Error ? e.message : 'Rotate failed';
		}
	}

	function openEditTab(id: typeof editTab) {
		editTab = id;
		if (id === 'grading' && !grading && !gradingLoading) void loadGrading();
	}

	const evalStatusCls: Record<string, string> = {
		ok: 'text-up border-up/30 bg-up/10',
		pending: 'text-info border-info/30 bg-info/10',
		infra_error: 'text-warn border-warn/30 bg-warn/10',
		expired: 'text-stone-500 border-stone-700 bg-stone-800/40'
	};

	function addEditFlag() {
		editFlags = [...editFlags, { name: `Flag ${editFlags.length + 1}`, flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '', has_value: false }];
	}
	async function saveEditFlag(f: any, i: number) {
		subError = ''; savingSub = 'flag' + i;
		const body: any = { name: f.name, points: Number(f.points) || 0, flag_type: f.flag_type || 'static', dynamic_flag_prefix: f.dynamic_flag_prefix || '' };
		// only send the value when one was typed - blank keeps the stored flag (update) and is required on create
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

	async function addExternalHandout() {
		if (!editingChallenge || !externalHandout.name.trim() || !externalHandout.url.trim()) return;
		subError = '';
		subUploading = true;
		try {
			await api.createAttachmentLink(editingChallenge.id, {
				name: externalHandout.name.trim(),
				url: externalHandout.url.trim(),
				sha256: externalHandout.sha256.trim(),
				description: externalHandout.description.trim()
			});
			const response = await api.listAttachments(editingChallenge.id);
			editAttachments = response?.attachments ?? [];
			externalHandout = { name: '', url: '', sha256: '', description: '' };
			flash('External handout added');
		} catch (e) {
			subError = e instanceof Error ? e.message : 'Failed to add external handout';
		} finally {
			subUploading = false;
		}
	}

	function addEditService() {
		editingChallenge.services = [...(editingChallenge.services || []), blankService(editingChallenge.services?.length || 0)];
	}

	function removeEditService(index: number) {
		editingChallenge.services = (editingChallenge.services || []).filter((_service: any, serviceIndex: number) => serviceIndex !== index);
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
			const delivery = editingChallenge.delivery_type || (editingChallenge.resource_type === 'vm' ? 'vm' : 'container');
			const payload: any = {
				name: editingChallenge.name,
				description: editingChallenge.description,
				sub_description: editingChallenge.sub_description || '',
				difficulty: editingChallenge.difficulty,
				base_points: editingChallenge.base_points,
				score_type: editingChallenge.scoring_mode === 'graded' ? 'static' : editingChallenge.score_type || 'static',
				score_minimum: editingChallenge.scoring_mode === 'graded' || editingChallenge.score_type !== 'dynamic' ? editingChallenge.base_points : editingChallenge.score_minimum,
				score_decay: editingChallenge.score_decay || 50,
				resource_type: delivery === 'vm' ? 'vm' : 'docker',
				delivery_type: delivery === 'vm' ? 'vm' : delivery === 'static' ? 'static' : delivery === 'external' ? 'external' : 'docker',
				author_name: editingChallenge.author_name || '',
				instance_timeout: editingChallenge.instance_timeout,
				max_extensions: editingChallenge.max_extensions,
				cooldown_minutes: editingChallenge.cooldown_minutes,
				scoring_mode: editingChallenge.scoring_mode || 'flag',
				arena_mode: delivery === 'static' || delivery === 'external' ? 'per_team' : editingChallenge.arena_mode || 'per_team',
				privesc: (delivery === 'container' || delivery === 'multi') && !!editingChallenge.privesc,
			};

			// category: send category_id (may be empty string to clear it) or fall back to name
			if (categories.length > 0) {
				payload.category_id = editingChallenge.category_id || null;
			} else if (editingChallenge.category_name) {
				payload.category = editingChallenge.category_name;
			}

			if (delivery !== 'vm') {
				payload.container_image = delivery === 'container' ? editingChallenge.container_image : '';
				payload.container_tag = editingChallenge.container_tag || 'latest';
				payload.container_platform = editingChallenge.container_platform;
				payload.cpu_limit = editingChallenge.cpu_limit;
				payload.memory_limit = editingChallenge.memory_limit;
				if (delivery === 'container' && Array.isArray(editingChallenge.exposed_ports)) {
					payload.exposed_ports = editingChallenge.exposed_ports.filter((p: any) => p.port > 0);
				} else {
					payload.exposed_ports = [];
				}
				payload.services = delivery === 'multi' ? (editingChallenge.services || []).map(servicePayload) : [];
			}

			if (delivery === 'vm') {
				payload.vm_template_id = editingChallenge.vm_template_id || null;
				payload.vm_timeout_minutes = editingChallenge.vm_timeout_minutes;
				payload.vm_max_extensions = editingChallenge.vm_max_extensions;
				payload.vm_extension_minutes = editingChallenge.vm_extension_minutes;
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
		infrastructureTimer = setInterval(() => {
			if (activeTab === 'infrastructure' && !infrastructureRefreshing) loadInfrastructure();
		}, 10000);
	});
	onDestroy(() => {
		if (infrastructureTimer) clearInterval(infrastructureTimer);
	});

	async function loadInfrastructure() {
		infrastructureRefreshing = true;
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
			infrastructureUpdatedAt = new Date();
		} catch (e) {
			infrastructureError = e instanceof Error ? e.message : 'Failed to load infrastructure data';
		} finally {
			infrastructureRefreshing = false;
		}
	}

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

			await loadInfrastructure();

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

	async function loadAnnouncements() {
		announcementsLoading = true;
		announcementsError = '';
		try {
			const response = await api.getAdminAnnouncements();
			announcements = response.announcements;
		} catch (e) {
			announcementsError = e instanceof Error ? e.message : 'Failed to load announcements';
		} finally {
			announcementsLoading = false;
		}
	}

	async function createAnnouncement() {
		if (!announcementForm.title.trim() || !announcementForm.body.trim()) {
			announcementsError = 'Title and message are required.';
			return;
		}
		announcementSaving = true;
		announcementsError = '';
		try {
			await api.createAdminAnnouncement({
				title: announcementForm.title,
				body: announcementForm.body,
				severity: announcementForm.severity,
				audience: announcementForm.audience,
				href: announcementForm.href || undefined,
				publish_at: announcementForm.publish_at ? new Date(announcementForm.publish_at).toISOString() : undefined,
				expires_at: announcementForm.expires_at ? new Date(announcementForm.expires_at).toISOString() : undefined,
				pinned: announcementForm.pinned
			});
			announcementForm = { title: '', body: '', severity: 'info', audience: 'all', href: '', publish_at: '', expires_at: '', pinned: false };
			await loadAnnouncements();
			window.dispatchEvent(new Event('notifications:changed'));
		} catch (e) {
			announcementsError = e instanceof Error ? e.message : 'Failed to create announcement';
		} finally {
			announcementSaving = false;
		}
	}

	async function cancelAnnouncement(item: AdminAnnouncement) {
		if (!(await confirmDialog({
			title: 'Cancel announcement',
			message: `Remove “${item.title}” from every participant inbox? The audit record is retained.`,
			confirmLabel: 'Cancel announcement', danger: true
		}))) return;
		try {
			await api.cancelAdminAnnouncement(item.id);
			await loadAnnouncements();
			window.dispatchEvent(new Event('notifications:changed'));
		} catch (e) {
			announcementsError = e instanceof Error ? e.message : 'Failed to cancel announcement';
		}
	}

	function announcementStatus(item: AdminAnnouncement) {
		if (item.cancelled_at) return 'Cancelled';
		if (item.expires_at && Date.parse(item.expires_at) <= Date.now()) return 'Expired';
		if (Date.parse(item.publish_at) > Date.now()) return 'Scheduled';
		return 'Live';
	}

	function setTab(id: string) {
		activeTab = id;
		if (id === 'teams' && teams.length === 0) {
			loadTeams();
		}
		if (id === 'communications' && announcements.length === 0) {
			loadAnnouncements();
		}
		if (id === 'infrastructure') {
			loadInfrastructure();
		}
	}

	async function savePlatformSettings() {
		if (settingsError || eventWindowError || historyEndError) return;
		savingSettings = true;
		settingsError = '';
		try {
			const writableSettings = { ...platformSettings };
			delete writableSettings['branding.logo_key'];
			delete writableSettings['branding.logo_mime'];
			await api.updatePlatformSettings(writableSettings);
			settingsChanged = false;
			await refreshPlatformInfo();
		} catch (e) {
			settingsError = e instanceof Error ? e.message : 'Failed to save settings';
			alertDialog({ title: 'Error', message: settingsError });
		} finally {
			savingSettings = false;
		}
	}

	function updateSetting(key: string, value: any) {
		platformSettings = { ...platformSettings, [key]: value };
		settingsChanged = true;
		settingsError = '';
	}

	async function loadEconomyPolicy() {
		if (economyPolicyDocument) return economyPolicyDocument;
		economyPolicyLoading = true;
		try {
			economyPolicyDocument = await api.getAdminEconomyPolicy();
			return economyPolicyDocument;
		} catch (e) {
			settingsError = e instanceof Error ? e.message : 'Failed to load Ledger policy';
			return null;
		} finally {
			economyPolicyLoading = false;
		}
	}

	async function toggleEconomyPolicy() {
		if (!showEconomyPolicy && !(await loadEconomyPolicy())) return;
		showEconomyPolicy = !showEconomyPolicy;
	}

	async function downloadEconomyPolicy() {
		const document = await loadEconomyPolicy();
		if (!document) return;
		const blob = new Blob([`${JSON.stringify(document, null, 2)}\n`], { type: 'application/json' });
		const href = URL.createObjectURL(blob);
		const anchor = window.document.createElement('a');
		anchor.href = href;
		anchor.download = `ledger-v${document.version}.json`;
		anchor.click();
		URL.revokeObjectURL(href);
	}

	function economyRuleLabel(key: string) {
		return key.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
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

	function handleEventTimeInput(e: Event, key: 'event.start_at' | 'event.end_at' | 'scoreboard.history_end_at') {
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
	$: historyEndError = (() => {
		const value = platformSettings['scoreboard.history_end_at'];
		if (value == null || value === '') return '';
		return typeof value === 'string' && Number.isFinite(Date.parse(value))
			? ''
			: 'Enter a valid score history cutoff.';
	})();

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
		imageInspection = null;
		imageInspectionError = '';
		showCreateModal = true;
	}

	async function inspectChallengeImage() {
		imageInspection = null;
		imageInspectionError = '';
		const image = newChallenge.docker_image.trim();
		if (!image) {
			imageInspectionError = 'Enter a Docker Hub image first.';
			return;
		}
		imageInspecting = true;
		try {
			const inspection = await api.inspectRegistryImage(image, newChallenge.container_platform);
			imageInspection = inspection;
			newChallenge.docker_image = inspection.immutable_reference;
			if (!newChallenge.container_platform && inspection.selected_platform) {
				newChallenge.container_platform = `${inspection.selected_platform.os}/${inspection.selected_platform.architecture}`;
			}
		} catch (e) {
			imageInspectionError = e instanceof Error ? e.message : 'Could not inspect the image.';
		} finally {
			imageInspecting = false;
		}
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

	async function openUserDetail(user: any) {
		selectedUserSeed = user;
		selectedUserDetail = null;
		userDetailError = '';
		userDetailLoading = true;
		try {
			selectedUserDetail = await api.getAdminUserDetail(user.id);
		} catch (e) {
			userDetailError = e instanceof Error ? e.message : 'Failed to load participant details';
		} finally {
			userDetailLoading = false;
		}
	}

	async function revokeUserSessions() {
		if (!selectedUserSeed || !(await confirmDialog({
			title: 'Sign out all sessions',
			message: `Sign out ${selectedUserDetail?.user?.username ?? selectedUserSeed.username} on every device?`,
			confirmLabel: 'Sign out all',
			danger: true
		}))) return;
		userDossierAction = 'all';
		try {
			await api.revokeUserSessions(selectedUserSeed.id);
			selectedUserDetail = await api.getAdminUserDetail(selectedUserSeed.id);
		} catch (e) {
			alertDialog({ title: 'Unable to revoke sessions', message: e instanceof Error ? e.message : 'Failed to revoke sessions' });
		} finally {
			userDossierAction = '';
		}
	}

	async function revokeUserSession(sessionId: string) {
		if (!selectedUserSeed || !(await confirmDialog({
			title: 'Revoke session',
			message: 'Sign this device out immediately?',
			confirmLabel: 'Revoke',
			danger: true
		}))) return;
		userDossierAction = sessionId;
		try {
			await api.revokeUserSession(selectedUserSeed.id, sessionId);
			selectedUserDetail = await api.getAdminUserDetail(selectedUserSeed.id);
		} catch (e) {
			alertDialog({ title: 'Unable to revoke session', message: e instanceof Error ? e.message : 'Failed to revoke session' });
		} finally {
			userDossierAction = '';
		}
	}

	async function openTeamDetail(team: any) {
		selectedTeamSeed = team;
		selectedTeamDetail = null;
		teamDetailError = '';
		teamDetailLoading = true;
		try {
			selectedTeamDetail = await api.getAdminTeamDetail(team.id);
		} catch (e) {
			teamDetailError = e instanceof Error ? e.message : 'Failed to load team details';
		} finally {
			teamDetailLoading = false;
		}
	}

	async function adjustTeamCredit(event: CustomEvent<{ amount: number; kind: string; note: string }>) {
		if (!selectedTeamSeed || teamCreditAdjusting) return;
		teamCreditAdjusting = true;
		teamDetailError = '';
		try {
			const result = await api.applyAdminTeamCredit(selectedTeamSeed.id, event.detail);
			const seed = { ...selectedTeamSeed, ledger_credits: result.balance_after };
			selectedTeamSeed = seed;
			await Promise.all([openTeamDetail(seed), loadTeams()]);
		} catch (e) {
			teamDetailError = e instanceof Error ? e.message : 'Failed to adjust team credits';
		} finally {
			teamCreditAdjusting = false;
		}
	}

	async function refreshTeamDossier() {
		if (!selectedTeamSeed) return;
		await Promise.all([openTeamDetail(selectedTeamSeed), loadTeams()]);
	}

	async function addTeamMember(event: CustomEvent<{ username: string }>) {
		if (!selectedTeamSeed || teamDossierAction) return;
		teamDossierAction = 'add-member';
		teamDetailError = '';
		try {
			await api.addAdminTeamMember(selectedTeamSeed.id, event.detail);
			await refreshTeamDossier();
		} catch (e) {
			teamDetailError = e instanceof Error ? e.message : 'Failed to add team member';
		} finally {
			teamDossierAction = '';
		}
	}

	async function removeTeamMember(event: CustomEvent<any>) {
		if (!selectedTeamSeed || teamDossierAction) return;
		const member = event.detail;
		if (!(await confirmDialog({ title: 'Remove team member', message: `Remove ${member.username} from ${selectedTeamSeed.name}?`, confirmLabel: 'Remove', danger: true }))) return;
		teamDossierAction = `remove-${member.id}`;
		teamDetailError = '';
		try {
			await api.removeAdminTeamMember(selectedTeamSeed.id, member.id);
			await refreshTeamDossier();
		} catch (e) {
			teamDetailError = e instanceof Error ? e.message : 'Failed to remove team member';
		} finally {
			teamDossierAction = '';
		}
	}

	async function stopTeamInstance(event: CustomEvent<any>) {
		if (!selectedTeamSeed || teamDossierAction) return;
		const instance = event.detail;
		if (!(await confirmDialog({ title: 'Force stop instance', message: `Stop ${instance.challenge_name} for ${instance.username || selectedTeamSeed.name}? Their session will end immediately.`, confirmLabel: 'Force stop', danger: true }))) return;
		teamDossierAction = `stop-${instance.id}`;
		teamDetailError = '';
		try {
			await api.forceStopAdminInstance(instance.id);
			await Promise.all([refreshTeamDossier(), loadInfrastructure()]);
		} catch (e) {
			teamDetailError = e instanceof Error ? e.message : 'Failed to stop instance';
		} finally {
			teamDossierAction = '';
		}
	}

	async function openChallengeDetail(challenge: any) {
		selectedChallengeSeed = challenge;
		selectedChallengeDetail = null;
		challengeDetailError = '';
		challengeDetailLoading = true;
		try {
			selectedChallengeDetail = await api.getAdminChallengeDetail(challenge.id);
		} catch (e) {
			challengeDetailError = e instanceof Error ? e.message : 'Failed to load challenge details';
		} finally {
			challengeDetailLoading = false;
		}
	}

	async function warnParticipant(userId: string, username: string) {
		const message = await promptDialog({
			title: `Warn ${username}`,
			message: 'The participant receives this as a pinned private organizer notification.',
			placeholder: 'Explain the policy concern and the action they should take.'
		});
		if (message === null || !message.trim()) return;
		actionLoading = userId;
		try {
			await api.warnUser(userId, message.trim());
			await alertDialog({ title: 'Warning sent', message: `${username} received a private organizer warning.` });
		} catch (e) {
			await alertDialog({ title: 'Error', message: e instanceof Error ? e.message : 'Failed to warn participant' });
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

	function paginationSummary(page: number, total: number) {
		if (total === 0) return '0 results';
		const first = (page - 1) * adminPageSize + 1;
		const last = Math.min(page * adminPageSize, total);
		return `${first.toLocaleString()}–${last.toLocaleString()} of ${total.toLocaleString()}`;
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

	function activateRow(event: KeyboardEvent, action: () => void) {
		if (event.key !== 'Enter' && event.key !== ' ') return;
		event.preventDefault();
		action();
	}

	function formatDate(timestamp: number): string {
		return formatLocalDateLong(timestamp, 'seconds');
	}

	async function handleCreateChallenge() {
		uploadLoading = true;
		uploadError = '';
		uploadProgress = 0;
		attachmentUploadStatus = '';
		const configureGraderAfterCreate = newChallenge.scoring_mode === 'graded';

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
			const common = {
				name: newChallenge.name,
				description: newChallenge.description,
				sub_description: newChallenge.sub_description,
				author_name: newChallenge.author_name,
				difficulty: newChallenge.difficulty,
				base_points: newChallenge.base_points,
				score_type: newChallenge.scoring_mode === 'graded' ? 'static' : newChallenge.score_type,
				score_minimum: newChallenge.scoring_mode === 'graded' || newChallenge.score_type !== 'dynamic' ? newChallenge.base_points : newChallenge.score_minimum,
				score_decay: newChallenge.score_decay,
				scoring_mode: newChallenge.scoring_mode,
				arena_mode: newChallenge.type === 'download' || newChallenge.type === 'external' ? 'per_team' : newChallenge.arena_mode,
				privesc: newChallenge.type === 'container' || newChallenge.type === 'multi' ? newChallenge.privesc : false,
				...(categoryId ? { category_id: categoryId } : {}),
				...(categoryName ? { category: categoryName } : {}),
				flags: configureGraderAfterCreate ? [] : newChallenge.flags.map((flag, index) => ({
					name: flag.name, flag: flag.flag, points: Number(flag.points) || 0,
					sort_order: index + 1, flag_type: flag.flag_type || 'static',
					dynamic_flag_prefix: flag.dynamic_flag_prefix || ''
				}))
			};

			if (newChallenge.type === 'ova') {
				// OVA challenges must reference an already-converted template (Infrastructure →
				// Templates). Direct-in-modal OVA upload is gone: it produced dead challenges.
				if (newChallenge.vm_source === 'template' && newChallenge.vm_template_id) {
					const result = await api.createAdminChallenge({
						...common,
						challenge_type: 'vm',
						delivery_type: 'vm',
						vm_template_id: newChallenge.vm_template_id,
						vcpu: newChallenge.vm_vcpu,
						memory_mb: newChallenge.vm_memory_mb,
						vm_timeout_minutes: newChallenge.vm_timeout_minutes,
						vm_max_extensions: newChallenge.vm_max_extensions,
						vm_extension_minutes: newChallenge.vm_extension_minutes,
						cooldown_minutes: newChallenge.cooldown_minutes
					});
					createdChallengeId = result?.id;
				} else {
					throw new Error('Select an existing VM template. To use a new OVA, upload it under Infrastructure → Templates first, then create the challenge here.');
				}
			} else {
				// container and download-only are both resource_type "docker"; a download-only
				// challenge has no image + no ports (files are added as attachments).
				const isDownload = newChallenge.type === 'download' || newChallenge.type === 'external';
				const isMulti = newChallenge.type === 'multi';
				const result = await api.createAdminChallenge({
					...common,
					challenge_type: 'docker',
					delivery_type: newChallenge.type === 'external' ? 'external' : newChallenge.type === 'download' ? 'static' : 'docker',
					container_image: isDownload || isMulti ? '' : newChallenge.docker_image,
					container_platform: isDownload ? '' : newChallenge.container_platform,
					exposed_ports: isDownload || isMulti ? [] : newChallenge.exposed_ports.filter(p => p.port > 0),
					services: isMulti ? newChallenge.services.map(servicePayload) : [],
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

			// challenge was created - close modal and reset form regardless of attachment failures
			showCreateModal = false;
			await loadDashboard();
			const createdChallenge = configureGraderAfterCreate ? challenges.find((challenge) => challenge.id === createdChallengeId) : null;

			newChallenge = {
				name: '',
				description: '',
				sub_description: '',
				author_name: '',
				category: '',
				category_id: '',
				newCategoryName: '',
				difficulty: 'easy',
				base_points: 100,
				score_type: 'static',
				score_minimum: 100,
				score_decay: 50,
				flag: '',
				flags: [{ name: 'User Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }, { name: 'Root Flag', flag: '', points: 50, flag_type: 'static', dynamic_flag_prefix: '' }],
				type: 'container',
				scoring_mode: 'flag',
				arena_mode: 'per_team',
				privesc: false,
				docker_image: '',
				container_platform: '',
				exposed_ports: [{ port: 1337, protocol: 'tcp', service: 'tcp' }],
				services: [],
				ova_url: '',
				vm_template_id: '',
				vm_source: 'template',
				vm_vcpu: 1,
				vm_memory_mb: 1024,
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
			if (createdChallenge) {
				openEditModal(createdChallenge);
				openEditTab('grading');
			}

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
		{ id: 'event', label: 'Event', icon: 'mdi:calendar-star' },
		{ id: 'data', label: 'Data', icon: 'mdi:database-export-outline' },
		{ id: 'challenges', label: 'Challenges', icon: 'mdi:flag-variant-outline' },
		{ id: 'users', label: 'Users', icon: 'mdi:account-group-outline' },
		{ id: 'teams', label: 'Teams', icon: 'mdi:account-multiple-outline' },
		{ id: 'communications', label: 'Comms', icon: 'mdi:bullhorn-outline' },
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
		<PageHeader title="Admin">
			<div slot="actions">
				{#if activeTab === 'challenges'}
					<div class="flex flex-wrap gap-2"><button on:click={() => showRegistryImportModal = true} class={btnGhost}><Icon icon="mdi:package-variant-closed-plus" class="w-3.5 h-3.5 shrink-0" />Import registry</button><button on:click={openCreateChallenge} class={btnPrimary}><Icon icon="mdi:plus" class="w-3.5 h-3.5 shrink-0" />New Challenge</button></div>
				{:else if activeTab === 'users'}
					<div class="flex flex-wrap gap-2"><button type="button" on:click={openPeopleImport} class={btnGhost}><Icon icon="mdi:file-import-outline" class="h-3.5 w-3.5" />Import people</button><button type="button" on:click={openParticipantModal} class={btnPrimary}><Icon icon="mdi:account-plus-outline" class="h-3.5 w-3.5" />Add participant</button></div>
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
			<div class="scrollbar-hidden border-b border-stone-800 mb-8 overflow-x-auto">
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
				<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
					<h2 class="text-xl font-semibold text-stone-100">Overview</h2>
					<div class="flex flex-wrap gap-2"><button type="button" on:click={() => setTab('event')} class={btnGhost}>Configure event</button></div>
				</div>

				<div class="mb-6 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
					{#each [
						{ label: 'Participants', value: stats?.total_users || 0, icon: 'mdi:account-outline' },
						{ label: 'Teams', value: stats?.total_teams || 0, icon: 'mdi:account-multiple-outline' },
						{ label: 'Published', value: stats?.published_challenges || 0, icon: 'mdi:flag-checkered' },
						{ label: 'Drafts', value: stats?.draft_challenges || 0, icon: 'mdi:file-document-edit-outline' },
						{ label: 'Live workloads', value: stats?.active_instances || 0, icon: 'mdi:cube-outline' },
						{ label: 'Submissions', value: stats?.total_submissions || 0, icon: 'mdi:send-check-outline' }
					] as stat}
						<div class="rounded-lg border border-stone-800 bg-stone-900/40 p-4">
							<div class="flex items-center justify-between gap-2"><p class="metadata-label text-stone-500">{stat.label}</p><OpticalIcon icon={stat.icon} size={15} box={16} className="text-stone-700" /></div>
							<p class="mt-2 text-2xl font-semibold text-stone-100 tabular-nums">{stat.value.toLocaleString()}</p>
						</div>
					{/each}
				</div>

				<div class="mb-6 grid items-start gap-6 xl:grid-cols-2">
					<Card title="Competition posture" bodyClass="p-4">
						<div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
							{#each [
								{ label: 'Event', value: $platformInfo?.event?.phase ?? 'unscheduled', enabled: $platformInfo?.event?.phase === 'live' },
								{ label: 'Registration', value: $platformInfo?.registration_mode ?? 'unknown', enabled: $platformInfo?.registration_mode !== 'disabled' },
								{ label: 'Scoreboard', value: $platformInfo?.scoreboard_enabled === false ? 'hidden' : 'public', enabled: $platformInfo?.scoreboard_enabled !== false },
								{ label: 'Economy', value: $platformInfo?.economy_enabled ? 'enabled' : 'disabled', enabled: !!$platformInfo?.economy_enabled },
								{ label: 'Market Pulse', value: $platformInfo?.market_pulse_enabled ? 'enabled' : 'disabled', enabled: !!$platformInfo?.market_pulse_enabled },
								{ label: 'Arena', value: $platformInfo?.arena_enabled ? 'enabled' : 'disabled', enabled: !!$platformInfo?.arena_enabled }
							] as item}
								<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">{item.label}</p><p class="mt-1 flex items-center gap-2 text-xs capitalize {item.enabled ? 'text-up' : 'text-stone-400'}"><span class="h-1.5 w-1.5 rounded-full {item.enabled ? 'bg-up' : 'bg-stone-600'}"></span>{item.value}</p></div>
							{/each}
						</div>
						<div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-stone-800 pt-4 text-xs text-stone-500"><span>{stats?.published_challenges || 0} of {stats?.total_challenges || 0} challenges released</span><span>{(stats?.total_solves || 0).toLocaleString()} successful solves</span></div>
					</Card>

					<Card title="Runtime pulse" bodyClass="p-4">
						<div class="flex items-start justify-between gap-4"><div><p class="text-3xl font-semibold text-stone-100 tabular-nums">{infraStats?.nodes?.online || 0}<span class="text-base font-normal text-stone-600">/{infraStats?.nodes?.total || 0}</span></p><p class="mt-1 text-xs text-stone-500">runtime nodes online</p></div><div class="text-right text-xs text-stone-500"><p>{infraStats?.instances?.running || 0} active workloads</p><p class="mt-1">{infraStats?.resources?.vcpu?.used || 0}/{infraStats?.resources?.vcpu?.total || 0} vCPU reserved</p></div></div>
						<div class="mt-5 grid grid-cols-2 gap-3">
							<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">Memory reserved</p><p class="mt-1 text-sm text-stone-300 tabular-nums">{infraStats?.resources?.memory_gb?.used || 0}/{infraStats?.resources?.memory_gb?.total || 0} GB</p></div>
							<div class="rounded-md border border-stone-800 bg-stone-950/50 p-3"><p class="metadata-label text-stone-600">Last sync</p><p class="mt-1 text-sm text-stone-300">{infrastructureUpdatedAt ? infrastructureUpdatedAt.toLocaleTimeString() : 'Connecting'}</p></div>
						</div>
						<button type="button" on:click={() => setTab('infrastructure')} class="mt-4 text-xs font-medium text-amber-500 hover:text-amber-400">Inspect nodes and workloads →</button>
					</Card>
				</div>

				<div class="grid items-start gap-6 lg:grid-cols-2">
					<Card title="Recent participants" bodyClass="p-0">
						{#if users.length === 0}<EmptyState icon="mdi:account-off-outline" text="No participants yet." />{:else}<div class="divide-y divide-stone-800/60">{#each users.slice(0, 6) as user}<button type="button" on:click={() => openUserDetail(user)} class="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-stone-900/60"><div class="flex min-w-0 items-center gap-3"><div class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-stone-800 text-xs font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</div><div class="min-w-0"><p class="truncate text-sm text-stone-200">{user.username}</p><p class="truncate text-xs text-stone-600">{user.email}</p></div></div><span class="shrink-0 text-xs text-stone-600" title={instantTitle(user.created_at, 'seconds')}>{formatDate(user.created_at)}</span></button>{/each}</div>{/if}
					</Card>

					<Card title="Most solved challenges" bodyClass="p-0">
						{#if challenges.length === 0}<EmptyState icon="mdi:flag-outline" text="No challenges yet." />{:else}<div class="divide-y divide-stone-800/60">{#each [...challenges].sort((a, b) => (b.total_solves || 0) - (a.total_solves || 0)).slice(0, 6) as challenge}<button type="button" on:click={() => openChallengeDetail(challenge)} class="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-stone-900/60"><div class="min-w-0"><p class="truncate text-sm text-stone-200">{challenge.name}</p><div class="mt-1.5 flex items-center gap-1.5"><span class="inline-flex rounded-full border px-2 py-0.5 text-[0.65rem] capitalize {difficultyClass(challenge.difficulty)}">{challenge.difficulty}</span><span class="text-[11px] text-stone-600">{deliveryLabel(challenge)}</span></div></div><span class="shrink-0 text-xs text-stone-400 tabular-nums">{challenge.total_solves || 0} solves</span></button>{/each}</div>{/if}
					</Card>
				</div>
			{/if}

			{#if activeTab === 'event'}
				<EventSetup
					settings={platformSettings}
					{challenges}
					saving={savingSettings}
					changed={settingsChanged}
					error={settingsError}
					update={updateSetting}
					save={savePlatformSettings}
				/>
			{:else if activeTab === 'data'}
				<DataWorkspace initialEntity={dataImportEntity} economyEnabled={$platformInfo?.economy_enabled ?? false} />
			{:else if activeTab === 'challenges'}
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
							<div
								class="cursor-pointer rounded-lg border border-stone-800 bg-stone-900/40 p-4 transition-colors hover:border-stone-700 hover:bg-stone-900/70 focus:outline-none focus:ring-1 focus:ring-amber-500/50"
								role="button"
								tabindex="0"
								on:click={() => openChallengeDetail(challenge)}
								on:keydown={(event) => activateRow(event, () => openChallengeDetail(challenge))}
							>
								<div class="flex items-start justify-between gap-3 mb-3">
									<div class="min-w-0">
										<p class="text-sm font-medium text-stone-200">{challenge.name}</p>
										<div class="flex items-center flex-wrap gap-1.5 mt-1.5 leading-none">
											<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}"><span class="badge-label">{challenge.difficulty}</span></span>
										<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
											<OpticalIcon icon={deliveryIcon(challenge)} size={12} box={12} /><span class="badge-label">{deliveryLabel(challenge)}</span>
											</span>
										</div>
									</div>
									<span class="inline-flex items-center gap-1.5 text-xs leading-none shrink-0 {challenge.status === 'published' ? 'text-up' : 'text-warn'}">
										<span class="w-1.5 h-1.5 rounded-full {challenge.status === 'published' ? 'bg-up' : 'bg-warn'}"></span>
										<span class="optical-label">{challenge.status === 'published' ? 'Published' : 'Draft'}</span>
									</span>
								</div>
								<div class="flex items-center justify-between text-xs text-stone-500 mb-3 tabular-nums">
									<span>{challenge.score_type === 'dynamic' ? `${challenge.base_points}–${challenge.score_minimum}` : challenge.base_points} pts</span>
									<span>{challenge.total_solves || 0} solves</span>
								</div>
								<div class="flex items-center gap-2 pt-3 border-t border-stone-800">
									<button
										on:click|stopPropagation={() => openEditModal(challenge)}
										on:keydown|stopPropagation
										class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-stone-300 border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
										disabled={actionLoading === challenge.id}
										title="Edit challenge"
									>
										<Icon icon="mdi:pencil" class="w-3 h-3 shrink-0" />
										Edit
									</button>
									{#if challenge.status === 'draft'}
										<button
											on:click|stopPropagation={() => publishChallenge(challenge)}
											on:keydown|stopPropagation
											class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-up border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
											disabled={actionLoading === challenge.id}
											title="Publish challenge"
										>
											<Icon icon="mdi:rocket-launch-outline" class="w-3 h-3 shrink-0" />
											Publish
										</button>
									{:else}
										<button
											on:click|stopPropagation={() => unpublishChallenge(challenge)}
											on:keydown|stopPropagation
											class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs leading-none font-medium text-warn border border-stone-700 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
											disabled={actionLoading === challenge.id}
											title="Unpublish challenge"
										>
											<Icon icon="mdi:eye-off-outline" class="w-3 h-3 shrink-0" />
											Unpublish
										</button>
									{/if}
									<button
										on:click|stopPropagation={() => deleteChallenge(challenge)}
										on:keydown|stopPropagation
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
											<tr
												class="cursor-pointer border-b border-stone-800/60 transition-colors hover:bg-stone-800/30 focus:bg-stone-800/30 focus:outline-none"
												role="button"
												tabindex="0"
											on:click={() => openChallengeDetail(challenge)}
											on:keydown={(event) => activateRow(event, () => openChallengeDetail(challenge))}
											>
												<td class="px-4 py-2.5">
												<span class="text-stone-200">{challenge.name}</span>
													{#if challenge.scoring_mode === 'graded'}
														<span class="ml-1.5 inline-flex items-center gap-1 rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[0.65rem] leading-none text-amber-500 align-middle" title="Graded challenge">
															<OpticalIcon icon="mdi:gauge" size={11} box={11} /><span class="badge-label">Graded</span>
														</span>
													{:else if challenge.score_type === 'dynamic'}
														<span class="ml-1.5 text-[0.65rem] text-stone-500">Dynamic</span>
													{/if}
												</td>
												<td class="px-4 py-2.5">
													<span class="inline-flex items-center px-2 py-0.5 rounded-full border text-[0.7rem] leading-none capitalize {difficultyClass(challenge.difficulty)}"><span class="badge-label">{challenge.difficulty}</span></span>
												</td>
												<td class="px-4 py-2.5">
											<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[0.7rem] leading-none {resourceClass(challenge.resource_type)}">
												<OpticalIcon icon={deliveryIcon(challenge)} size={12} box={12} /><span class="badge-label">{deliveryLabel(challenge)}</span>
													</span>
												</td>
												<td class="px-4 py-2.5 text-right text-stone-200 tabular-nums">{challenge.score_type === 'dynamic' ? `${challenge.base_points}–${challenge.score_minimum}` : challenge.base_points}</td>
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
															on:click|stopPropagation={() => openEditModal(challenge)}
															on:keydown|stopPropagation
															class="p-2 text-stone-400 hover:text-stone-100 hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
															disabled={actionLoading === challenge.id}
															title="Edit"
														>
															<Icon icon="mdi:pencil" class="w-4 h-4" />
														</button>
														{#if challenge.status === 'draft'}
															<button
																on:click|stopPropagation={() => publishChallenge(challenge)}
																on:keydown|stopPropagation
																class="p-2 text-up hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
																disabled={actionLoading === challenge.id}
																title="Publish"
															>
																<Icon icon="mdi:rocket-launch-outline" class="w-4 h-4" />
															</button>
														{:else}
															<button
																on:click|stopPropagation={() => unpublishChallenge(challenge)}
																on:keydown|stopPropagation
																class="p-2 text-warn hover:bg-stone-800/40 rounded-md transition-colors disabled:opacity-50"
																disabled={actionLoading === challenge.id}
																title="Unpublish"
															>
																<Icon icon="mdi:eye-off-outline" class="w-4 h-4" />
															</button>
														{/if}
														<button
															on:click|stopPropagation={() => deleteChallenge(challenge)}
															on:keydown|stopPropagation
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
				<div class="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
					<div class="relative w-full sm:max-w-md">
						<Icon icon="mdi:magnify" class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-stone-600" />
						<input
							type="search"
							bind:value={usersQuery}
							on:input={() => usersPage = 1}
							placeholder="Search username, email, or role..."
							class="w-full rounded-md border border-stone-800 bg-stone-950 py-2 pl-9 pr-3 text-sm text-stone-200 focus:border-stone-500 focus:outline-none"
						/>
					</div>
					<span class="text-xs tabular-nums text-stone-600">{filteredUsers.length.toLocaleString()} participants</span>
				</div>
				{#if users.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:account-off-outline" text="No users yet." />
					</Card>
				{:else if filteredUsers.length === 0}
					<Card hasHeader={false}>
						<EmptyState icon="mdi:account-search-outline" text="No participants match that search." />
					</Card>
				{:else}
					<div class="lg:hidden space-y-3">
						{#each pagedUsers as user}
							<div
								class="cursor-pointer rounded-lg border border-stone-800 bg-stone-900/40 p-4 transition-colors hover:border-stone-700 hover:bg-stone-900/70 focus:outline-none focus:ring-1 focus:ring-amber-500/50"
								role="button"
								tabindex="0"
								on:click={() => openUserDetail(user)}
								on:keydown={(event) => activateRow(event, () => openUserDetail(user))}
							>
								<div class="flex items-center gap-3 mb-3">
									<div class="w-10 h-10 bg-stone-800 rounded-full flex items-center justify-center shrink-0">
										<span class="text-sm font-medium text-stone-400">{user.username.charAt(0).toUpperCase()}</span>
									</div>
									<div class="flex-1 min-w-0">
										<p class="truncate text-sm font-medium text-stone-200">{user.username}</p>
										<p class="text-xs text-stone-500 truncate">{user.email}</p>
									</div>
									<span class="text-xs {user.role === 'admin' ? 'text-amber-500/90' : 'text-stone-400'}">{user.role}</span>
								</div>
								<div class="flex items-center justify-between text-xs text-stone-500 pt-3 border-t border-stone-800 tabular-nums">
									<span>{user.total_score || 0} points</span>
									<span title={instantTitle(user.created_at, 'seconds')}>Joined {formatDate(user.created_at)}</span>
								</div>
								<div class="flex items-center justify-end gap-3 mt-3 pt-3 border-t border-stone-800">
									<select
										value={user.role}
										on:click|stopPropagation
										on:keydown|stopPropagation
										on:change={(e) => changeUserRole(user.id, e.currentTarget.value)}
										disabled={actionLoading === user.id}
										class="text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-1 text-stone-200 focus:outline-none focus:border-stone-500"
									>
										<option value="user">User</option>
										<option value="admin">Admin</option>
									</select>
									<button
										on:click|stopPropagation={() => toggleBan(user)}
										on:keydown|stopPropagation
										disabled={actionLoading === user.id}
										class="text-xs {user.is_banned ? 'text-up' : 'text-warn'} hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
										title={user.is_banned ? 'Unban user' : 'Ban user'}
									>
										{user.is_banned ? 'Unban' : 'Ban'}
									</button>
									<button
										on:click|stopPropagation={() => deleteUser(user.id)}
										on:keydown|stopPropagation
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
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{filteredUsers.length.toLocaleString()}</span>
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
									{#each pagedUsers as user}
											<tr
												class="cursor-pointer border-b border-stone-800/60 transition-colors hover:bg-stone-800/30 focus:bg-stone-800/30 focus:outline-none"
												role="button"
												tabindex="0"
												on:click={() => openUserDetail(user)}
												on:keydown={(event) => activateRow(event, () => openUserDetail(user))}
											>
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
														on:click|stopPropagation
														on:keydown|stopPropagation
															on:change={(e) => changeUserRole(user.id, e.currentTarget.value)}
															disabled={actionLoading === user.id}
															class="text-xs bg-stone-950 border border-stone-800 rounded-md px-2 py-1 text-stone-200 focus:outline-none focus:border-stone-500"
														>
															<option value="user">User</option>
															<option value="admin">Admin</option>
														</select>
													<button
														on:click|stopPropagation={() => toggleBan(user)}
														on:keydown|stopPropagation
															disabled={actionLoading === user.id}
															class="text-xs {user.is_banned ? 'text-up' : 'text-warn'} hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
															title={user.is_banned ? 'Unban user' : 'Ban user'}
														>
															{user.is_banned ? 'Unban' : 'Ban'}
														</button>
													<button
														on:click|stopPropagation={() => deleteUser(user.id)}
														on:keydown|stopPropagation
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
					<div class="mt-4 flex items-center justify-between gap-3 rounded-md border border-stone-800 bg-stone-950/40 px-3 py-2">
						<span class="text-xs tabular-nums text-stone-500">{paginationSummary(usersPage, filteredUsers.length)}</span>
						<div class="flex items-center gap-2">
							<button type="button" class={btnGhost} disabled={usersPage === 1} on:click={() => usersPage -= 1}><Icon icon="mdi:chevron-left" class="h-4 w-4" /> Previous</button>
							<span class="hidden text-xs tabular-nums text-stone-600 sm:inline">Page {usersPage} of {usersPageCount}</span>
							<button type="button" class={btnGhost} disabled={usersPage === usersPageCount} on:click={() => usersPage += 1}>Next <Icon icon="mdi:chevron-right" class="h-4 w-4" /></button>
						</div>
					</div>
				{/if}
			{/if}

			{#if activeTab === 'teams'}
				<div class="flex items-center gap-2 mb-4">
					<input
						type="text"
						bind:value={teamsQuery}
						on:input={() => { teamsPage = 1; loadTeams(); }}
						placeholder="Search teams by name..."
						class="flex-1 text-sm bg-stone-950 border border-stone-800 rounded-md px-3 py-2 text-stone-200 focus:outline-none focus:border-stone-500"
					/>
					<select
						bind:value={teamsSort}
						on:change={() => { teamsPage = 1; loadTeams(); }}
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
					<div class="space-y-3 lg:hidden">
						{#each pagedTeams as team}
							<div
								class="cursor-pointer rounded-lg border border-stone-800 bg-stone-900/40 p-4 transition-colors hover:border-stone-700 hover:bg-stone-900/70 focus:outline-none focus:ring-1 focus:ring-amber-500/50"
								role="button"
								tabindex="0"
								on:click={() => openTeamDetail(team)}
								on:keydown={(event) => activateRow(event, () => openTeamDetail(team))}
							>
								<div class="flex items-start justify-between gap-3">
									<div class="min-w-0"><p class="truncate text-sm font-medium text-stone-200">{team.name}</p><p class="mt-1 text-xs text-stone-600">{team.member_count} members · joined {team.created_at ? formatDate(team.created_at) : '—'}</p></div>
									<Icon icon="mdi:chevron-right" class="h-5 w-5 shrink-0 text-stone-600" />
								</div>
								<div class="mt-4 grid grid-cols-3 gap-3 border-y border-stone-800 py-3 text-xs">
									<div><p class="metadata-label text-stone-600">Solved</p><p class="mt-1 text-stone-300">{team.challenge_solves ?? 0}</p></div>
									<div><p class="metadata-label text-stone-600">{$platformInfo?.economy_enabled ? 'Ledger' : 'Score'}</p><p class="mt-1 text-amber-500">{Math.round($platformInfo?.economy_enabled ? (team.ledger_points ?? team.total_score ?? 0) : (team.total_score ?? team.legacy_score ?? 0)).toLocaleString()}</p></div>
									<div><p class="metadata-label text-stone-600">Capacity</p><p class="mt-1 text-stone-300">{team.member_count}/{team.max_members ?? '∞'}</p></div>
								</div>
								<div class="mt-3 flex items-center justify-between gap-3">
									<span class="truncate font-mono text-[11px] text-stone-600">Join {team.join_code}</span>
									<div class="flex items-center gap-3">
										<button type="button" class="text-xs text-stone-400 hover:text-stone-200" on:click|stopPropagation={() => renameTeam(team)} on:keydown|stopPropagation>Rename</button>
										<button type="button" class="text-xs text-stone-500 hover:text-stone-300" on:click|stopPropagation={() => rotateTeamCode(team)} on:keydown|stopPropagation disabled={actionLoading === team.id}>Rotate code</button>
										<button type="button" class="text-xs text-down hover:underline disabled:opacity-50" on:click|stopPropagation={() => disbandTeam(team)} on:keydown|stopPropagation disabled={actionLoading === team.id}>{actionLoading === team.id ? '…' : 'Disband'}</button>
									</div>
								</div>
							</div>
						{/each}
					</div>

					<div class="hidden lg:block">
						<Card title="Teams" bodyClass="">
							<span slot="meta" class="text-stone-500 text-xs tabular-nums">{teams.length.toLocaleString()}</span>
							<div class="overflow-x-auto">
								<table class="w-full min-w-[900px] text-sm">
									<thead>
										<tr class="metadata-label border-b border-stone-800 text-stone-500">
											<th class="px-4 py-2.5 text-left">Team</th>
											<th class="px-4 py-2.5 text-right">Members</th>
											<th class="px-4 py-2.5 text-right">Solved</th>
											<th class="px-4 py-2.5 text-right">{$platformInfo?.economy_enabled ? 'Ledger' : 'Score'}</th>
											<th class="hidden px-4 py-2.5 text-right md:table-cell">Max</th>
											<th class="hidden px-4 py-2.5 text-left md:table-cell">Join code</th>
											<th class="hidden px-4 py-2.5 text-right xl:table-cell">Created</th>
											<th class="px-4 py-2.5 text-right">Actions</th>
									</tr>
								</thead>
								<tbody>
									{#each pagedTeams as team}
											<tr
												class="cursor-pointer border-b border-stone-800/60 transition-colors hover:bg-stone-800/30 focus:bg-stone-800/30 focus:outline-none"
												role="button"
												tabindex="0"
												on:click={() => openTeamDetail(team)}
												on:keydown={(event) => activateRow(event, () => openTeamDetail(team))}
											>
												<td class="px-4 py-3"><div class="flex items-center gap-2"><span class="font-medium text-stone-200">{team.name}</span><Icon icon="mdi:chevron-right" class="h-4 w-4 text-stone-700" /></div></td>
												<td class="px-4 py-3 text-right text-stone-300 tabular-nums">{team.member_count}</td>
												<td class="px-4 py-3 text-right text-stone-200 tabular-nums">{team.challenge_solves ?? 0}</td>
												<td class="px-4 py-3 text-right text-amber-500 tabular-nums">{Math.round($platformInfo?.economy_enabled ? (team.ledger_points ?? team.total_score ?? 0) : (team.total_score ?? team.legacy_score ?? 0)).toLocaleString()}</td>
												<td class="hidden px-4 py-3 text-right text-stone-400 tabular-nums md:table-cell" on:click|stopPropagation on:keydown|stopPropagation><button type="button" class="hover:underline" on:click={() => editTeamMax(team)}>{team.max_members == null ? '∞' : team.max_members}</button></td>
												<td class="hidden px-4 py-3 md:table-cell" on:click|stopPropagation on:keydown|stopPropagation><span class="font-mono text-xs text-stone-400">{team.join_code}</span><button type="button" class="ml-2 text-xs text-stone-600 hover:text-stone-300 disabled:opacity-50" on:click={() => rotateTeamCode(team)} disabled={actionLoading === team.id}>rotate</button></td>
												<td class="hidden px-4 py-3 text-right text-stone-500 tabular-nums xl:table-cell" title={team.created_at ? instantTitle(team.created_at, 'seconds') : ''}>{team.created_at ? formatDate(team.created_at) : '—'}</td>
												<td class="px-4 py-3" on:click|stopPropagation on:keydown|stopPropagation>
													<div class="flex items-center justify-end gap-3">
														<button type="button" class="text-xs text-stone-400 hover:text-stone-200" on:click={() => renameTeam(team)}>Rename</button>
														<button type="button" class="text-xs text-down hover:underline disabled:opacity-50" on:click={() => disbandTeam(team)} disabled={actionLoading === team.id}>{actionLoading === team.id ? '…' : 'Disband'}</button>
													</div>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</Card>
					</div>
					<div class="mt-4 flex items-center justify-between gap-3 rounded-md border border-stone-800 bg-stone-950/40 px-3 py-2">
						<span class="text-xs tabular-nums text-stone-500">{paginationSummary(teamsPage, teams.length)}</span>
						<div class="flex items-center gap-2">
							<button type="button" class={btnGhost} disabled={teamsPage === 1} on:click={() => teamsPage -= 1}><Icon icon="mdi:chevron-left" class="h-4 w-4" /> Previous</button>
							<span class="hidden text-xs tabular-nums text-stone-600 sm:inline">Page {teamsPage} of {teamsPageCount}</span>
							<button type="button" class={btnGhost} disabled={teamsPage === teamsPageCount} on:click={() => teamsPage += 1}>Next <Icon icon="mdi:chevron-right" class="h-4 w-4" /></button>
						</div>
					</div>
				{/if}
			{/if}

			{#if activeTab === 'communications'}
				{#if announcementsError}
					<div class="mb-6 flex items-center justify-between gap-3 rounded-lg border border-down/20 bg-down/[0.06] px-4 py-3 text-sm text-down" aria-live="polite">
						<span>{announcementsError}</span>
						<button type="button" on:click={loadAnnouncements} class="shrink-0 underline underline-offset-2">Retry</button>
					</div>
				{/if}
				<div class="grid gap-6 xl:grid-cols-[minmax(320px,0.8fr)_minmax(0,1.2fr)]">
					<Card bodyClass="p-4">
						<div slot="header">
							<h2 class="flex items-center gap-2 text-sm font-semibold text-stone-200">
								<OpticalIcon icon="mdi:bullhorn-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">New Announcement</span>
							</h2>
						</div>
						<div class="space-y-4">
							<label class="block">
								<span class={labelCls}>Title</span>
								<input bind:value={announcementForm.title} maxlength="160" placeholder="Challenge update" class="w-full {fieldCls}" />
							</label>
							<label class="block">
								<span class={labelCls}>Message</span>
								<textarea bind:value={announcementForm.body} maxlength="5000" rows="6" placeholder="Tell participants what changed and what they need to do." class="w-full resize-y {fieldCls}"></textarea>
							</label>
							<div class="grid grid-cols-2 gap-3">
								<label class="block">
									<span class={labelCls}>Severity</span>
									<select bind:value={announcementForm.severity} class="w-full {fieldCls}">
										<option value="info">Info</option>
										<option value="success">Resolved</option>
										<option value="warning">Warning</option>
										<option value="critical">Critical</option>
									</select>
								</label>
								<label class="block">
									<span class={labelCls}>Audience</span>
									<select bind:value={announcementForm.audience} class="w-full {fieldCls}">
										<option value="all">Everyone signed in</option>
										<option value="participants">Participants</option>
										<option value="staff">Staff</option>
									</select>
								</label>
							</div>
							<label class="block">
								<span class={labelCls}>Action link <span class="text-stone-600">(optional)</span></span>
								<input bind:value={announcementForm.href} maxlength="1000" placeholder="/challenges/example or https://status…" class="w-full {fieldCls}" />
							</label>
							<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
								<label class="block">
									<span class={labelCls}>Publish at <span class="text-stone-600">(blank = now)</span></span>
									<input type="datetime-local" bind:value={announcementForm.publish_at} class="w-full {fieldCls}" />
								</label>
								<label class="block">
									<span class={labelCls}>Expires at <span class="text-stone-600">(optional)</span></span>
									<input type="datetime-local" bind:value={announcementForm.expires_at} class="w-full {fieldCls}" />
								</label>
							</div>
							<label class="flex items-center gap-3 rounded-md border border-stone-800 bg-stone-950/35 p-3">
								<input type="checkbox" bind:checked={announcementForm.pinned} />
								<span class="text-sm text-stone-300">Pin to top</span>
							</label>
							<button on:click={createAnnouncement} disabled={announcementSaving || !announcementForm.title.trim() || !announcementForm.body.trim()} class="w-full {btnPrimary}">
								<Icon icon={announcementSaving ? 'mdi:loading' : 'mdi:send-outline'} class="h-4 w-4 {announcementSaving ? 'animate-spin' : ''}" />
								{announcementForm.publish_at ? 'Schedule announcement' : 'Publish announcement'}
							</button>
						</div>
					</Card>

					<Card bodyClass="p-0">
						<div slot="header" class="flex items-center justify-between gap-3">
							<h2 class="text-sm font-semibold text-stone-200">Announcement history</h2>
							<button on:click={loadAnnouncements} disabled={announcementsLoading} class="text-xs text-stone-500 hover:text-stone-300"><Icon icon="mdi:refresh" class="inline h-4 w-4 {announcementsLoading ? 'animate-spin' : ''}" /></button>
						</div>
						{#if announcementsLoading && announcements.length === 0}
							<div class="flex items-center justify-center gap-2 py-14 text-sm text-stone-500"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Loading announcements…</div>
						{:else if announcements.length === 0}
							<EmptyState icon="mdi:bullhorn-outline" text="No announcements yet." />
						{:else}
							<div class="divide-y divide-stone-800/70">
								{#each announcements as item}
									<div class="p-4 {item.cancelled_at ? 'opacity-55' : ''}">
										<div class="flex items-start justify-between gap-4">
											<div class="min-w-0">
												<div class="flex flex-wrap items-center gap-2">
													<p class="text-sm font-medium text-stone-200">{item.title}</p>
													<span class="rounded-full bg-stone-900 px-2 py-0.5 text-[10px] text-stone-500">{announcementStatus(item)}</span>
													<span class="text-[10px] uppercase tracking-wider text-stone-600">{item.audience} · {item.severity}</span>
												</div>
												<p class="mt-2 whitespace-pre-wrap text-xs leading-relaxed text-stone-400">{item.body}</p>
												<p class="mt-2 text-[11px] text-stone-600">Publishes {formatLocalDateTimeWithZone(item.publish_at)}{item.expires_at ? ` · expires ${formatLocalDateTimeWithZone(item.expires_at)}` : ''}</p>
											</div>
											{#if !item.cancelled_at && (!item.expires_at || Date.parse(item.expires_at) > Date.now())}
												<button on:click={() => cancelAnnouncement(item)} class="shrink-0 text-xs text-down hover:underline">Cancel</button>
											{/if}
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</Card>
				</div>
				<MailWorkspace />
			{/if}

			{#if activeTab === 'infrastructure'}
				<div class="mb-4 flex items-center justify-between gap-3">
					<div class="flex items-center gap-3"><h2 class="text-sm font-semibold text-stone-200">Runtime capacity</h2><span class="text-[10px] text-stone-600">10 s · reserved</span></div>
					<div class="flex items-center gap-2 text-xs text-stone-500"><span class="h-1.5 w-1.5 rounded-full {infrastructureError ? 'bg-down' : 'bg-up'}"></span>{infrastructureRefreshing ? 'Refreshing…' : infrastructureUpdatedAt ? `Updated ${infrastructureUpdatedAt.toLocaleTimeString()}` : 'Connecting…'}<button type="button" on:click={loadInfrastructure} disabled={infrastructureRefreshing} class="ml-2 text-stone-300 hover:text-stone-100 disabled:opacity-50" aria-label="Refresh infrastructure"><Icon icon="mdi:refresh" class="h-4 w-4 {infrastructureRefreshing ? 'animate-spin' : ''}" /></button></div>
				</div>
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
						<p class="metadata-label text-stone-500">Reserved vCPU</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.vcpu?.used || 0}/{infraStats?.resources?.vcpu?.total || 0}
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.vcpu?.available || 0} available</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">Reserved memory</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">
							{infraStats?.resources?.memory_gb?.used || 0}/{infraStats?.resources?.memory_gb?.total || 0} GB
						</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">{infraStats?.resources?.memory_gb?.available || 0} GB free</p>
					</div>
					<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-4">
						<p class="metadata-label text-stone-500">Running workloads</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums mt-1">{infraStats?.instances?.running || 0}</p>
						<p class="text-xs text-stone-400 mt-1 tabular-nums">of {infraStats?.instances?.total || 0} active</p>
					</div>
				</div>

				<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
					<Card title="Runtime nodes" bodyClass="">
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
												<span class="text-[0.7rem] text-stone-500 border border-stone-700 px-1.5 py-0.5 rounded-full shrink-0">{node.runtime || 'vm'}{node.architecture ? ` · ${node.architecture}` : ''}</span>
											</div>
											{#if node.can_delete !== false}
												<button
													on:click={() => deleteNode(node.id)}
													disabled={actionLoading === node.id}
													class="text-stone-500 hover:text-down transition-colors disabled:opacity-50 shrink-0"
													title="Delete node"
												>
													<Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
												</button>
											{/if}
										</div>
									<div class="text-xs text-stone-500 tabular-nums">
											<span>{node.ip_address}</span>
											<span class="mx-2 text-stone-700">•</span>
											<span>{node.active_vms}/{node.max_vms} {node.runtime === 'docker' ? 'workloads' : 'VMs'}</span>
											<span class="mx-2 text-stone-700">•</span>
										<span>{node.used_vcpu}/{node.total_vcpu} vCPU</span>
									</div>
									<p class="mt-1 text-[11px] text-stone-600">{node.runtime === 'docker' ? 'Swarm' : node.last_heartbeat ? `Heartbeat ${formatLocalDateTimeWithZone(node.last_heartbeat, 'seconds')}` : 'No heartbeat'}</p>
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
												<td class="px-4 py-2.5 text-stone-400 font-mono text-xs">{instance.ip_address || '-'}</td>
												<td class="px-4 py-2.5">
													<span class="text-xs {instance.status === 'running' ? 'text-up' : 'text-warn'}">{instance.status}</span>
												</td>
												<td class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums">
													{instance.expires_at
														? formatLocalDateTimeWithZone(instance.expires_at, 'seconds')
														: '-'}
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
											<td class="px-4 py-2.5 text-stone-400 font-mono text-xs">{instance.ip_address || '-'}</td>
											<td class="px-4 py-2.5">
												<span class="text-xs {instance.status === 'running' ? 'text-up' : 'text-warn'}">{instance.status}</span>
											</td>
											<td class="px-4 py-2.5 text-right text-xs text-stone-500 tabular-nums">
												{instance.expires_at
													? formatLocalDateTimeWithZone(instance.expires_at, 'seconds')
													: '-'}
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
				<nav class="sticky top-2 z-20 mb-6 rounded-lg border border-stone-800 bg-stone-950/95 p-2 shadow-xl shadow-black/20 backdrop-blur" aria-label="Settings sections">
					<div class="mb-2 flex items-center justify-between gap-3 px-2 pt-1"><p class="metadata-label text-stone-600">Settings</p>{#if settingsChanged}<span class="shrink-0 rounded-full bg-warn/10 px-2 py-1 text-[11px] text-warn">Unsaved changes</span>{/if}</div>
					<div class="grid grid-cols-2 gap-1 md:grid-cols-4">
						{#each [
							...($platformInfo?.economy_enabled ? [{ href: '#settings-ledger', label: 'Economy', icon: 'mdi:scale-balance' }] : []),
							{ href: '#settings-instances', label: 'Runtime', icon: 'mdi:timer-outline' },
							{ href: '#settings-access', label: 'Access', icon: 'mdi:shield-key-outline' },
							{ href: '#settings-platform', label: 'Competition', icon: 'mdi:tune-variant' }
						] as link}
							<a href={link.href} class="flex min-w-0 items-center gap-2 rounded-md border border-transparent px-2.5 py-2 text-left transition-colors hover:border-stone-800 hover:bg-stone-900"><OpticalIcon icon={link.icon} size={15} box={16} className="shrink-0 text-stone-500" /><span class="truncate text-xs font-medium text-stone-300">{link.label}</span></a>
						{/each}
					</div>
				</nav>
				<div class="space-y-6 {settingsError ? 'pointer-events-none select-none opacity-40' : ''}" aria-disabled={settingsError ? 'true' : undefined}>
					{#if settingsChanged}
						<div class="flex justify-end">
							<button on:click={savePlatformSettings} disabled={savingSettings || !!eventWindowError || !!historyEndError} class={btnPrimary}>
								{#if savingSettings}
									<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
								{:else}
									<Icon icon="mdi:content-save-outline" class="w-3.5 h-3.5 shrink-0" />
								{/if}
								Save Settings
							</button>
						</div>
					{/if}

					{#if $platformInfo?.economy_enabled && $platformInfo?.economy_policy}
						<Card elementId="settings-ledger" bodyClass="p-4 scroll-mt-32">
							<div slot="header">
								<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
									<OpticalIcon icon="mdi:scale-balance" size={14} box={14} className="text-stone-500" />
									<span class="optical-label">Ledger policy</span>
								</h2>
							</div>
							<div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
								<div class="flex items-center gap-3">
									<div class="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-amber-500/10 text-amber-500"><Icon icon="mdi:bank-outline" class="h-5 w-5" /></div>
									<div>
										<div class="flex flex-wrap items-center gap-2"><p class="text-sm font-medium text-stone-200">{$platformInfo.economy_policy.name}</p><span class="rounded-full border border-stone-800 px-2 py-0.5 text-[10px] text-stone-500">v{$platformInfo.economy_policy.version}</span></div>
									</div>
								</div>
								<div class="flex flex-wrap items-center gap-2">
									<span class="w-fit rounded-full px-2.5 py-1 text-[11px] font-medium {$platformInfo.economy_policy.customized ? 'bg-amber-500/10 text-amber-400' : 'bg-emerald-500/10 text-emerald-400'}">{$platformInfo.economy_policy.customized ? 'Organizer customized' : 'Canonical policy'}</span>
									<button type="button" class={btnGhost} on:click={toggleEconomyPolicy} disabled={economyPolicyLoading}>{economyPolicyLoading ? 'Loading…' : showEconomyPolicy ? 'Hide rules' : 'View rules'}</button>
									<button type="button" class={btnGhost} on:click={downloadEconomyPolicy} disabled={economyPolicyLoading}><Icon icon="mdi:download-outline" class="h-4 w-4" /> Download JSON</button>
								</div>
							</div>
							{#if showEconomyPolicy && economyPolicyDocument}
								<div class="mt-4 rounded-lg border border-stone-800 bg-stone-950/40 p-3">
									<div class="mb-3 flex flex-wrap items-center justify-between gap-2"><p class="text-xs font-medium text-stone-300">Active rules</p><p class="text-[11px] text-stone-600">Difficulty order: {economyPolicyDocument.difficulty_order.join(' → ')}</p></div>
									<div class="grid gap-px overflow-hidden rounded-md border border-stone-800 bg-stone-800 sm:grid-cols-2 xl:grid-cols-3">
										{#each Object.entries(economyPolicyDocument.parameters) as [key, value]}
											<div class="flex min-w-0 items-start justify-between gap-3 bg-stone-950 px-3 py-2.5"><span class="text-[11px] text-stone-500">{economyRuleLabel(key)}</span><code class="max-w-[60%] break-words text-right text-[11px] text-stone-300">{Array.isArray(value) ? value.join(' · ') : value}</code></div>
										{/each}
									</div>
								</div>
							{/if}
							<details class="mt-4 border-t border-stone-800 pt-3"><summary class="cursor-pointer text-xs text-stone-500 hover:text-stone-300">Technical identity</summary><div class="mt-3 grid gap-2 text-[11px] sm:grid-cols-3"><div><p class="metadata-label text-stone-600">Internal policy ID</p><code class="mt-1 block break-all text-stone-500">{$platformInfo.economy_policy.id}</code></div><div><p class="metadata-label text-stone-600">Active checksum</p><code class="mt-1 block break-all text-stone-500">{$platformInfo.economy_policy.checksum}</code></div><div><p class="metadata-label text-stone-600">Canonical checksum</p><code class="mt-1 block break-all text-stone-500">{$platformInfo.economy_policy.canonical_checksum}</code></div></div></details>
						</Card>
					{/if}

					<Card elementId="settings-instances" bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:timer-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Instance Timeouts</span>
							</h2>
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

					<Card elementId="settings-access" bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:vpn" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">VPN Settings</span>
							</h2>
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

					<Card elementId="settings-platform" bodyClass="p-4">
						<div slot="header">
							<h2 class="text-sm leading-none font-semibold text-stone-200 flex items-center gap-2">
								<OpticalIcon icon="mdi:cog-outline" size={14} box={14} className="text-stone-500" />
								<span class="optical-label">Platform Settings</span>
							</h2>
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
								<span class={labelCls}>Score source</span>
								<select
									value={String(platformSettings['scoreboard.score_source'] ?? 'auto')}
									on:change={(e) => handleTextInput(e, 'scoreboard.score_source')}
									class="w-full {fieldCls}"
								>
									<option value="auto">Auto</option>
									<option value="standard">Standard</option>
									<option value="ledger">Ledger</option>
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
									<option value="true">Enabled</option>
									<option value="false">Disabled</option>
								</select>
							</label>
							<label class="block">
								<span class={labelCls}>Market Pulse <span class="normal-case tracking-normal text-stone-700">(requires Economy)</span></span>
								<select
									value={String(platformSettings.market_pulse_enabled ?? false)}
									on:change={(e) => handleSelectChange(e, 'market_pulse_enabled')}
									class="w-full {fieldCls}"
								>
									<option value="true">Enabled</option>
									<option value="false">Disabled</option>
								</select>
							</label>
							<div class="md:col-span-2 mt-1 border-t border-stone-800/70 pt-4">
								<div class="mb-3 flex items-start justify-between gap-3">
									<h3 class="text-sm font-medium text-stone-300">CTF window <span class="ml-2 text-xs font-normal text-stone-600">{browserTimeZone}</span></h3>
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
								<div class="mt-4 border-t border-stone-800/70 pt-4">
									<div class="mb-2 flex items-start justify-between gap-3">
										<span class={labelCls}>Score history cutoff <span class="normal-case tracking-normal text-stone-700">(defaults to CTF end)</span></span>
										{#if platformSettings['scoreboard.history_end_at']}
											<button type="button" on:click={() => updateSetting('scoreboard.history_end_at', '')} class="shrink-0 text-xs text-stone-500 transition-colors hover:text-stone-300">Clear</button>
										{/if}
									</div>
									<input
										type="datetime-local"
										step="60"
										value={datetimeLocalValue(platformSettings['scoreboard.history_end_at'])}
										on:input={(e) => handleEventTimeInput(e, 'scoreboard.history_end_at')}
										class="w-full {fieldCls} tabular-nums"
									/>
									{#if historyEndError}
										<p class="mt-2 text-xs text-down" aria-live="polite">{historyEndError}</p>
									{/if}
								</div>
							</div>
						</div>
					</Card>
				</div>
			{:else if activeTab === 'intel'}
				<SecurityIntelligence />
			{/if}
		{/if}
	</div>
</div>

{#if selectedUserSeed}
	<UserDossier
		seed={selectedUserSeed}
		detail={selectedUserDetail}
		loading={userDetailLoading}
		error={userDetailError}
		economyEnabled={$platformInfo?.economy_enabled ?? false}
		action={userDossierAction}
		on:close={() => { selectedUserSeed = null; selectedUserDetail = null; userDossierAction = ''; }}
		on:warn={() => warnParticipant(selectedUserSeed.id, selectedUserDetail?.user?.username ?? selectedUserSeed.username)}
		on:toggleban={() => selectedUserDetail?.user && toggleBan({ id: selectedUserSeed.id, username: selectedUserDetail.user.username, is_banned: selectedUserDetail.user.status === 'banned' })}
		on:revokeall={revokeUserSessions}
		on:revokesession={(event) => revokeUserSession(event.detail.id)}
	/>
{/if}

{#if selectedTeamSeed}
	<TeamDossier
		seed={selectedTeamSeed}
		detail={selectedTeamDetail}
		loading={teamDetailLoading}
		error={teamDetailError}
		adjusting={teamCreditAdjusting}
		action={teamDossierAction}
		economyEnabled={$platformInfo?.economy_enabled ?? false}
		on:close={() => { selectedTeamSeed = null; selectedTeamDetail = null; }}
		on:user={(event) => { const member = event.detail; selectedTeamSeed = null; selectedTeamDetail = null; void openUserDetail(member); }}
		on:credit={adjustTeamCredit}
		on:addmember={addTeamMember}
		on:removemember={removeTeamMember}
		on:stopinstance={stopTeamInstance}
	/>
{/if}

{#if selectedChallengeSeed}
	<ChallengeDossier
		seed={selectedChallengeSeed}
		detail={selectedChallengeDetail}
		loading={challengeDetailLoading}
		error={challengeDetailError}
		economyEnabled={$platformInfo?.economy_enabled ?? false}
		on:close={() => { selectedChallengeSeed = null; selectedChallengeDetail = null; }}
		on:edit={() => { const challenge = selectedChallengeSeed; selectedChallengeSeed = null; selectedChallengeDetail = null; openEditModal(challenge); }}
	/>
{/if}

{#if showParticipantModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close participant form" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showParticipantModal = false}></button>
		<div class="relative z-10 w-full max-w-xl overflow-hidden rounded-lg border border-stone-800 bg-stone-950 shadow-2xl" role="dialog" aria-modal="true" aria-label="Add participant">
			<div class="flex items-start justify-between gap-4 border-b border-stone-800 px-5 py-4">
				<h2 class="text-base font-semibold text-stone-100">Add participant</h2>
				<button type="button" on:click={() => showParticipantModal = false} class="p-1 text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button>
			</div>
			<form class="space-y-4 p-5" on:submit|preventDefault={createParticipant}>
				{#if participantError}<div class="rounded-md border border-down/20 bg-down/5 px-3 py-2.5 text-sm text-down">{participantError}</div>{/if}
				<div class="grid gap-4 sm:grid-cols-2">
					<label class="sm:col-span-2"><span class={labelCls}>Email</span><input type="email" bind:value={participantForm.email} required autocomplete="off" placeholder="participant@example.com" class="w-full {fieldCls}" /></label>
					<label><span class={labelCls}>Display name</span><input bind:value={participantForm.display_name} maxlength="100" autocomplete="off" placeholder="Avery Rao" class="w-full {fieldCls}" /></label>
					<label><span class={labelCls}>Username <span class="normal-case tracking-normal text-stone-600">(optional)</span></span><input bind:value={participantForm.username} maxlength="50" autocomplete="off" placeholder="Generated from email" class="w-full {fieldCls}" /></label>
					<label><span class={labelCls}>Role</span><select bind:value={participantForm.role} class="w-full {fieldCls}"><option value="user">Participant</option><option value="author">Challenge author</option></select></label>
					<label><span class={labelCls}>Account access</span><select bind:value={participantForm.provisioning} class="w-full {fieldCls}"><option value="activation_email">Activation link</option><option value="generated_credentials">Temporary credentials</option><option value="sso_only">SSO only</option></select></label>
				</div>
				<div class="flex items-center justify-between gap-3 border-t border-stone-800 pt-4"><button type="button" on:click={() => showParticipantModal = false} class={btnGhost}>Cancel</button><button type="submit" disabled={participantBusy || !participantForm.email.trim()} class={btnPrimary}>{participantBusy ? 'Creating…' : 'Create participant'}</button></div>
			</form>
		</div>
	</div>
{/if}

{#if showRegistryImportModal}
	<RegistryBulkImport {categories} onClose={() => showRegistryImportModal = false} onApplied={loadDashboard} />
{/if}

{#if showCreateModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<button type="button" aria-label="Close dialog" class="fixed inset-0 bg-stone-950/80 backdrop-blur-sm" on:click={() => showCreateModal = false}></button>
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-xl w-full max-w-6xl max-h-[94vh] flex flex-col shadow-2xl" role="dialog" aria-modal="true">
			<div class="p-4 sm:p-6 border-b border-stone-800 flex-shrink-0">
				<div class="flex items-center justify-between mb-4">
					<h2 class="text-lg font-semibold text-stone-100 flex items-center gap-2">
						<Icon icon="mdi:plus-circle-outline" class="w-5 h-5 text-stone-400" />
						Create Challenge
					</h2>
					<button on:click={() => showCreateModal = false} class="text-stone-500 hover:text-stone-200 transition-colors p-1">
						<Icon icon="mdi:close" class="w-5 h-5" />
					</button>
				</div>

				<div class="grid grid-cols-2 gap-1.5 p-1.5 bg-stone-900/40 border border-stone-800 rounded-lg sm:grid-cols-5">
					<button
						type="button"
						on:click={() => newChallenge.type = 'container'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'container' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:docker" class="w-3.5 h-3.5 shrink-0" />
						Container
					</button>
					<button
						type="button"
						on:click={() => { newChallenge.type = 'multi'; if (!newChallenge.services.length) newChallenge.services = [blankService(0), blankService(1)]; }}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'multi' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:server-network" class="w-3.5 h-3.5 shrink-0" />
						Multi-service
					</button>
					<button
						type="button"
						on:click={() => newChallenge.type = 'download'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'download' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:file-download-outline" class="w-3.5 h-3.5 shrink-0" />
						Files
					</button>
					<button
						type="button"
						on:click={() => newChallenge.type = 'external'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'external' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:open-in-new" class="w-3.5 h-3.5 shrink-0" />
						External
					</button>
					<button
						type="button"
						on:click={() => newChallenge.type = 'ova'}
						class="flex-1 flex items-center justify-center gap-2 py-2.5 rounded text-sm leading-none font-medium transition-colors {newChallenge.type === 'ova' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
					>
						<Icon icon="mdi:desktop-classic" class="w-3.5 h-3.5 shrink-0" />
						VM
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

				<form on:submit|preventDefault={handleCreateChallenge} class="p-4 sm:p-6 space-y-6">
					{#if uploadError}
						<div class="flex items-start gap-2 py-3 px-4 bg-down/10 border border-down/30 rounded-md text-down text-sm">
							<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
							{uploadError}
						</div>
					{/if}

					<section class="rounded-lg border border-stone-800 bg-stone-900/20 p-4 sm:p-5"><h3 class="mb-5 text-sm font-semibold text-stone-200">Basics</h3><div class="grid grid-cols-1 gap-5 md:grid-cols-2">
						<label class="block md:col-span-2">
							<span class={labelCls}>Name *</span>
							<input
								type="text"
								bind:value={newChallenge.name}
								required
								class="w-full {fieldCls}"
								placeholder="Challenge name"
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
									<option value="" disabled>Select category</option>
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
							<span class={labelCls}>{newChallenge.score_type === 'dynamic' ? 'Maximum points' : 'Points'} *</span>
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

						<label class="block">
							<span class={labelCls}>Author</span>
							<input type="text" bind:value={newChallenge.author_name} class="w-full {fieldCls}" placeholder="Author" />
						</label>

						{#if !($platformInfo?.economy_enabled ?? false)}
							<label class="block">
								<span class={labelCls}>Scoring</span>
								<select value={scoringModel(newChallenge)} on:change={(event) => setNewScoringModel(event.currentTarget.value)} class="w-full {fieldCls}">
									<option value="static">Static</option>
									<option value="dynamic">Dynamic</option>
									<option value="graded">Graded</option>
								</select>
							</label>
							{#if scoringModel(newChallenge) === 'dynamic'}
								<label><span class={labelCls}>Minimum points</span><input type="number" bind:value={newChallenge.score_minimum} min="0" max={newChallenge.base_points} class="w-full {fieldCls} tabular-nums" /></label>
								<label><span class={labelCls}>Decay (solves)</span><input type="number" bind:value={newChallenge.score_decay} min="1" class="w-full {fieldCls} tabular-nums" /></label>
							{/if}
						{:else if $platformInfo?.economy_enabled}
							<div><span class={labelCls}>Scoring</span><div class="flex h-[38px] items-center rounded-md border border-stone-800 bg-stone-950 px-3 text-sm text-stone-300">Ledger</div></div>
						{/if}

						<label class="block md:col-span-2">
							<span class={labelCls}>Target topology</span>
							<select bind:value={newChallenge.arena_mode} class="w-full {fieldCls}" disabled={newChallenge.type === 'download' || newChallenge.type === 'external'}>
								<option value="per_team">Isolated per team</option>
								<option value="shared">Shared</option>
							</select>
							</label>
					</div></section>

					{#if newChallenge.type === 'container' || newChallenge.type === 'multi' || newChallenge.type === 'download' || newChallenge.type === 'external'}
							<div class="space-y-5 rounded-lg border border-stone-800 bg-stone-900/20 p-4 sm:p-5"><h3 class="text-sm font-semibold text-stone-200">Delivery</h3>
								{#if newChallenge.type === 'multi'}
									<div class="space-y-3">
									<div class="flex items-center justify-between"><span class="metadata-label text-stone-400">Service roles</span><button type="button" on:click={addService} class="text-xs text-stone-400 hover:text-stone-200">+ Add service</button></div>
									{#each newChallenge.services as service, serviceIndex}
										<div class="space-y-3 rounded-lg border border-stone-800 bg-stone-900/20 p-4">
										<div class="grid gap-2 sm:grid-cols-[9rem_1fr_7rem_auto]"><input bind:value={service.name} required class="min-w-0 font-mono {fieldCls}" placeholder="app" /><input bind:value={service.image} required class="min-w-0 font-mono {fieldCls}" placeholder="ghcr.io/org/image" /><input bind:value={service.tag} class="min-w-0 font-mono {fieldCls}" placeholder="latest" /><button type="button" on:click={() => removeService(serviceIndex)} disabled={newChallenge.services.length === 1} class="justify-self-start p-2 text-stone-600 hover:text-down disabled:opacity-30 sm:justify-self-auto"><Icon icon="mdi:trash-can-outline" class="h-4 w-4" /></button></div>
											<div class="grid gap-3 sm:grid-cols-2"><label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={service.public} class="accent-amber-500" /> Public route</label><label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={service.egress} class="accent-amber-500" /> Internet egress</label></div>
											<div class="grid gap-3 sm:grid-cols-2"><label><span class={labelCls}>CPU</span><input bind:value={service.cpu_limit} class="w-full {fieldCls}" placeholder="1" /></label><label><span class={labelCls}>Memory</span><input bind:value={service.memory_limit} class="w-full {fieldCls}" placeholder="512Mi" /></label></div>
											<div><div class="mb-2 flex items-center justify-between"><span class={labelCls}>Ports</span><button type="button" on:click={() => service.ports = [...service.ports, { port: 0, protocol: 'tcp', service: 'tcp', internal: !service.public }]} class="text-xs text-stone-500 hover:text-stone-300">+ Port</button></div>{#each service.ports as port, portIndex}<div class="mb-2 flex flex-wrap items-center gap-2"><input type="number" bind:value={port.port} min="1" max="65535" class="w-24 {fieldCls}" /><select bind:value={port.service} class="min-w-36 flex-1 {fieldCls}"><option value="http">HTTP</option><option value="tcp">TCP</option></select><label class="flex items-center gap-1 text-xs text-stone-500"><input type="checkbox" bind:checked={port.internal} class="accent-amber-500" /> Internal only</label><button type="button" on:click={() => service.ports = service.ports.filter((_: any, index: number) => index !== portIndex)} class="p-1 text-stone-600 hover:text-down"><Icon icon="mdi:close" class="h-4 w-4" /></button></div>{/each}</div>
										<div class="grid gap-3 sm:grid-cols-2"><label><span class={labelCls}>Command arguments</span><textarea bind:value={service.command_text} rows="3" class="w-full font-mono {fieldCls}" placeholder="One argument per line&#10;--serve&#10;0.0.0.0"></textarea></label><label><span class={labelCls}>Environment</span><textarea bind:value={service.env_text} rows="3" class="w-full font-mono {fieldCls}" placeholder="KEY=value&#10;INTERNAL_URL=http://db:5432"></textarea></label></div>
										</div>
									{/each}
										<label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={newChallenge.privesc} class="accent-amber-500" /><span>SUID</span></label>
								</div>
							{/if}
							{#if newChallenge.type === 'container'}
							<div class="block">
								<div class="mb-1.5 flex items-center justify-between gap-3">
									<span class="metadata-label text-stone-400">Docker image *</span>
									<button type="button" on:click={inspectChallengeImage} disabled={imageInspecting || !newChallenge.docker_image.trim()} class="text-xs text-stone-400 transition-colors hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-40">
										{imageInspecting ? 'Inspecting…' : 'Inspect registry image'}
									</button>
								</div>
								<input
									type="text"
									bind:value={newChallenge.docker_image}
									on:input={() => { imageInspection = null; imageInspectionError = ''; }}
									required
									class="w-full font-mono {fieldCls}"
									placeholder="organization/challenge:release"
								/>
								{#if imageInspection}
									<div class="mt-3 rounded-md border border-up/20 bg-up/[0.05] px-3 py-2.5 text-xs">
										<div class="flex items-center gap-2 text-up"><Icon icon="mdi:check-circle-outline" class="h-4 w-4" /><span class="font-medium">Verified and pinned</span></div>
										<p class="mt-1 break-all font-mono text-stone-400">{imageInspection.immutable_reference}</p>
										<p class="mt-1 text-stone-500">{imageInspection.selected_platform?.os}/{imageInspection.selected_platform?.architecture}{imageInspection.size_bytes ? ` · ${formatFileSize(imageInspection.size_bytes)}` : ''}{imageInspection.rate_limit_remaining ? ` · ${imageInspection.rate_limit_remaining} pulls remaining` : ''}</p>
									</div>
								{:else if imageInspectionError}
									<div class="mt-3 flex items-start gap-2 rounded-md border border-down/20 bg-down/[0.05] px-3 py-2.5 text-xs text-down"><Icon icon="mdi:alert-circle-outline" class="mt-0.5 h-4 w-4 shrink-0" /><span>{imageInspectionError}</span></div>
								{/if}
							</div>
							<RegistryCredentials />

							<label class="block">
								<span class={labelCls}>Platform</span>
								<select bind:value={newChallenge.container_platform} on:change={() => { imageInspection = null; imageInspectionError = ''; }} class="w-full {fieldCls}">
									<option value="">Auto (native architecture)</option>
									<option value="linux/amd64">linux/amd64</option>
									<option value="linux/arm64">linux/arm64</option>
								</select>
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
													<option value="tcp">TCP</option>
													<option value="http">HTTP</option>
											</select>
											{#if newChallenge.exposed_ports.length > 1}
												<button type="button" on:click={() => newChallenge.exposed_ports = newChallenge.exposed_ports.filter((_, idx) => idx !== i)} class="p-1.5 text-stone-500 hover:text-down transition-colors">
													<Icon icon="mdi:close" class="w-4 h-4" />
												</button>
											{/if}
										</div>
									{/each}
								</div>
								</div>
								{#if newChallenge.type === 'container'}
									<label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={newChallenge.privesc} class="accent-amber-500" /><span>SUID</span></label>
							{/if}
							{/if}

							{#if newChallenge.scoring_mode === 'flag'}
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
										<div class="flex flex-wrap items-center gap-2">
											<input type="text" bind:value={fl.name} required class="min-w-40 flex-1 {fieldCls}" placeholder="Flag name" />
												<input type="number" bind:value={fl.points} min="0" class="w-20 tabular-nums {fieldCls}" placeholder="pts" />
											<select bind:value={fl.flag_type} class="min-w-28 flex-1 sm:flex-none {fieldCls}">
														<option value="static">Exact</option>
														<option value="regex">Pattern</option>
														<option value="dynamic">Per-instance</option>
												</select>
												{#if newChallenge.flags.length > 1}
													<button type="button" on:click={() => newChallenge.flags = newChallenge.flags.filter((_, idx) => idx !== i)} class="p-1 text-stone-500 hover:text-down transition-colors">
														<Icon icon="mdi:close" class="w-4 h-4" />
													</button>
												{/if}
											</div>
											{#if fl.flag_type === 'static'}
											<input type="text" bind:value={fl.flag} required class="w-full font-mono {fieldCls}" placeholder="flag&#123;value&#125;" />
										{:else if fl.flag_type === 'regex'}
							<input type="text" bind:value={fl.flag} required class="w-full font-mono {fieldCls}" placeholder="flag&#123;[a-f0-9-]+&#125;" />
											{:else}
							<input type="text" bind:value={fl.dynamic_flag_prefix} required class="w-full font-mono {fieldCls}" placeholder="CTF" />
											{/if}
										</div>
									{/each}
								</div>
								<p class="mt-2 text-xs tabular-nums {newChallenge.flags.reduce((sum, flag) => sum + (Number(flag.points) || 0), 0) === Number(newChallenge.base_points) ? 'text-stone-600' : 'text-warn'}">Total: {newChallenge.flags.reduce((sum, flag) => sum + (Number(flag.points) || 0), 0)} / {newChallenge.base_points}</p>
							</div>
							{/if}

							{#if newChallenge.type === 'container' || newChallenge.type === 'multi'}
							<div>
								<span class="metadata-label block text-stone-400 mb-3">Instance Settings</span>
								<div class="grid gap-3 sm:grid-cols-3">
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
							</div>
							{/if}
						</div>
					{:else if newChallenge.type === 'ova'}
						<div class="rounded-lg border border-stone-800 bg-stone-900/20 p-4 sm:p-5 space-y-5"><h3 class="text-sm font-semibold text-stone-200">Virtual machine</h3>
							<div>
								<span class={labelCls}>VM Source</span>
								<div class="flex gap-1 p-1 bg-stone-950 border border-stone-800 rounded-md">
									<button
										type="button"
										on:click={() => newChallenge.vm_source = 'template'}
										class="flex-1 inline-flex items-center justify-center py-2 px-3 rounded text-sm leading-none font-medium transition-colors {newChallenge.vm_source === 'template' ? 'bg-stone-800 text-stone-100' : 'text-stone-400 hover:text-stone-200'}"
									>
										<Icon icon="mdi:harddisk" class="w-3.5 h-3.5 shrink-0 mr-1" />
										Template
									</button>
								</div>
							</div>

							{#if newChallenge.vm_source === 'template'}
								<div>
									<span class={labelCls}>Select VM Template *</span>
									{#if templates.length === 0}
										<div class="p-4 bg-stone-900/40 border border-stone-800 rounded-md text-center">
											<Icon icon="mdi:alert-circle-outline" class="w-8 h-8 text-stone-500 mx-auto mb-2" />
											<p class="text-stone-400 text-sm">No templates available</p>
											<p class="text-stone-500 text-xs mt-1">Add a template under System</p>
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
									<p class="text-stone-500 text-xs mt-2">.ova · .qcow2 · .vmdk · 20 GB max</p>
								</div>
							{/if}

							<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
								<label class="block"><span class={labelCls}>vCPU reservation</span><input type="number" bind:value={newChallenge.vm_vcpu} min="1" max="64" class="w-full {fieldCls} tabular-nums" /></label>
								<label class="block"><span class={labelCls}>Memory (MB)</span><input type="number" bind:value={newChallenge.vm_memory_mb} min="256" step="256" class="w-full {fieldCls} tabular-nums" /></label>
								<label class="block"><span class={labelCls}>Run time (min)</span><input type="number" bind:value={newChallenge.vm_timeout_minutes} min="1" class="w-full {fieldCls} tabular-nums" /></label>
								<label class="block"><span class={labelCls}>Max extensions</span><input type="number" bind:value={newChallenge.vm_max_extensions} min="0" class="w-full {fieldCls} tabular-nums" /></label>
								<label class="block"><span class={labelCls}>Extension (min)</span><input type="number" bind:value={newChallenge.vm_extension_minutes} min="1" class="w-full {fieldCls} tabular-nums" /></label>
								<label class="block"><span class={labelCls}>Reset cooldown (min)</span><input type="number" bind:value={newChallenge.cooldown_minutes} min="0" class="w-full {fieldCls} tabular-nums" /></label>
							</div>

							{#if newChallenge.scoring_mode === 'flag'}
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
										<div class="flex flex-wrap items-center gap-3 mb-3">
												<input
													type="text"
													bind:value={flag.name}
											class="min-w-40 flex-1 {fieldCls}"
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
							{/if}
						</div>
					{/if}

					{#if newChallenge.type !== 'external'}
					<div class="rounded-lg border border-stone-800 bg-stone-900/20 p-4 sm:p-5 space-y-3">
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
									<div class="flex flex-wrap items-center gap-2 p-2 bg-stone-950 border border-stone-800 rounded-md">
										<Icon icon="mdi:file-outline" class="w-4 h-4 text-stone-400 shrink-0" />
										<div class="min-w-0 flex-1">
											<p class="text-xs text-stone-300 truncate">{attachment.file.name}</p>
											<p class="text-xs text-stone-600 tabular-nums">{formatFileSize(attachment.file.size)}</p>
										</div>
										<input
											type="text"
											bind:value={attachment.description}
											placeholder="Description (optional)"
										class="basis-full sm:basis-auto flex-1 min-w-0 px-2 py-1 bg-stone-950 border border-stone-800 rounded text-xs text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-500"
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
							<p class="text-xs text-stone-600">No files selected.</p>
						{/if}
						{#if attachmentUploadStatus}
								<p class="text-xs leading-none text-stone-400 flex items-center gap-1.5">
									<Icon icon="mdi:loading" class="w-3 h-3 shrink-0 animate-spin" />
								{attachmentUploadStatus}
							</p>
						{/if}
					</div>
					{/if}

					<div class="sticky bottom-0 z-20 flex gap-3 border-t border-stone-800 bg-stone-950/95 py-4 backdrop-blur">
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
								{newChallenge.scoring_mode === 'graded' ? 'Create and configure grader' : 'Create Challenge'}
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
		<div class="relative z-10 bg-stone-950 border border-stone-800 rounded-lg w-full max-w-5xl max-h-[94vh] overflow-y-auto" role="dialog" aria-modal="true">
			<div class="px-6 py-4 border-b border-stone-800 flex items-center justify-between sticky top-0 bg-stone-950 z-10">
				<div>
					<h2 class="text-lg font-semibold text-stone-100">Edit Challenge</h2>
					<p class="text-xs text-stone-500 mt-0.5">
						{editingChallenge.delivery_type === 'vm' ? 'Virtual machine' : editingChallenge.delivery_type === 'multi' ? 'Multi-service' : editingChallenge.delivery_type === 'static' ? 'Files' : editingChallenge.delivery_type === 'external' ? 'External' : 'Container'} ·
						<span class="{editingChallenge.status === 'published' ? 'text-up' : 'text-warn'}">{editingChallenge.status}</span>
						· {editingChallenge.slug}
					</p>
				</div>
				<button on:click={() => { showEditModal = false; editingChallenge = null; }} class="text-stone-500 hover:text-stone-200 transition-colors">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>

			<div class="px-3 sm:px-6 border-b border-stone-800 flex gap-1 bg-stone-950 overflow-x-auto">
				{#each [{ id: 'settings', label: 'Settings', n: 0 }, ...(editingChallenge.scoring_mode === 'flag' ? [{ id: 'flags', label: 'Flags', n: editFlags.length }] : []), { id: 'hints', label: 'Hints', n: editHints.length }, { id: 'files', label: 'Files', n: editAttachments.length }, ...(editingChallenge.scoring_mode === 'graded' ? [{ id: 'grading', label: 'Grading', n: 0 }] : [])] as t}
					<button type="button" on:click={() => openEditTab(t.id as typeof editTab)} class="px-3.5 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors {editTab === t.id ? 'border-amber-500 text-stone-100' : 'border-transparent text-stone-500 hover:text-stone-300'}">
						{t.label}{#if t.n}<span class="ml-1.5 text-xs tabular-nums {editTab === t.id ? 'text-amber-500' : 'text-stone-600'}">{t.n}</span>{/if}
					</button>
				{/each}
			</div>

			{#if subError}<div class="mx-6 mt-4 px-3 py-2 rounded-md bg-down/10 border border-down/30 text-down text-xs">{subError}</div>{/if}
			{#if subNote}<div class="mx-6 mt-4 px-3 py-2 rounded-md bg-up/10 border border-up/30 text-up text-xs">{subNote}</div>{/if}

			{#if editTab === 'settings'}
				<form on:submit|preventDefault={handleEditChallenge} class="p-4 sm:p-6 space-y-5">

				<label class="block">
					<span class={labelCls}>Name *</span>
					<input type="text" bind:value={editingChallenge.name} required class="w-full {fieldCls}" />
				</label>

				<label class="block">
					<span class={labelCls}>Description</span>
					<textarea bind:value={editingChallenge.description} rows="4" class="w-full {fieldCls} resize-none"></textarea>
				</label>

				<div class="grid gap-4 sm:grid-cols-2">
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
						<span class={labelCls}>{editingChallenge.score_type === 'dynamic' ? 'Maximum points' : 'Points'}</span>
						<input type="number" bind:value={editingChallenge.base_points} required min="1" class="w-full {fieldCls} tabular-nums" />
					</label>
					<label class="block">
						<span class={labelCls}>Author</span>
						<input type="text" bind:value={editingChallenge.author_name} placeholder="Author" class="w-full {fieldCls}" />
					</label>
					{#if !($platformInfo?.economy_enabled ?? false)}
						<label class="block"><span class={labelCls}>Scoring</span><select value={scoringModel(editingChallenge)} on:change={(event) => setEditScoringModel(event.currentTarget.value)} class="w-full {fieldCls}"><option value="static">Static</option><option value="dynamic">Dynamic</option><option value="graded">Graded</option></select></label>
						{#if scoringModel(editingChallenge) === 'dynamic'}
							<label class="block"><span class={labelCls}>Minimum points</span><input type="number" bind:value={editingChallenge.score_minimum} min="0" max={editingChallenge.base_points} class="w-full {fieldCls} tabular-nums" /></label>
							<label class="block"><span class={labelCls}>Decay (solves)</span><input type="number" bind:value={editingChallenge.score_decay} min="1" class="w-full {fieldCls} tabular-nums" /></label>
						{/if}
					{:else if $platformInfo?.economy_enabled}
						<div><span class={labelCls}>Scoring</span><div class="flex h-[38px] items-center rounded-md border border-stone-800 bg-stone-950 px-3 text-sm text-stone-300">Ledger</div></div>
					{/if}
					<label class="block">
						<span class={labelCls}>Delivery</span>
						<select bind:value={editingChallenge.delivery_type} on:change={() => editingChallenge.resource_type = editingChallenge.delivery_type === 'vm' ? 'vm' : 'docker'} class="w-full {fieldCls}">
							<option value="container">Single container</option>
							<option value="multi">Multi-service</option>
							<option value="static">Files</option>
							<option value="external">External</option>
							<option value="vm">Virtual machine</option>
						</select>
					</label>
					<label class="block">
						<span class={labelCls}>Topology</span>
						<select bind:value={editingChallenge.arena_mode} disabled={editingChallenge.delivery_type === 'static' || editingChallenge.delivery_type === 'external'} class="w-full {fieldCls}"><option value="per_team">Isolated per team</option><option value="shared">Shared</option></select>
					</label>
				</div>

				{#if editingChallenge.delivery_type === 'container'}
					<div class="border border-stone-800 rounded-lg p-4 space-y-4">
						<div class="flex items-center justify-between gap-3"><h3 class="metadata-label text-stone-400">Container</h3><label class="flex items-center gap-2 text-xs text-stone-500"><input type="checkbox" bind:checked={editingChallenge.privesc} class="accent-amber-500" /> SUID</label></div>

						<div class="grid gap-3 sm:grid-cols-3">
							<label class="block sm:col-span-2">
								<span class={labelCls}>Docker Image</span>
								<input type="text" bind:value={editingChallenge.container_image} required placeholder="ghcr.io/org/image" class="w-full font-mono {fieldCls}" />
							</label>
							<label class="block">
								<span class={labelCls}>Tag</span>
								<input type="text" bind:value={editingChallenge.container_tag} placeholder="latest" class="w-full font-mono {fieldCls}" />
							</label>
						</div>

						<div class="grid gap-3 sm:grid-cols-3">
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
								<div class="flex flex-wrap items-center gap-2 mb-2">
									<input type="number" bind:value={ep.port} placeholder="Port" min="1" max="65535" class="w-20 font-mono tabular-nums {fieldCls}" />
									<select bind:value={ep.service} class="flex-1 {fieldCls}">
										<option value="tcp">TCP</option>
										<option value="http">HTTP</option>
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
						</div>
					</div>
				{:else if editingChallenge.delivery_type === 'multi'}
					<div class="space-y-4 rounded-lg border border-stone-800 p-4">
						<div class="flex items-center justify-between gap-3"><h3 class="metadata-label text-stone-400">Services</h3><button type="button" on:click={addEditService} class="shrink-0 text-xs text-stone-400 hover:text-stone-200">+ Add service</button></div>
						{#each editingChallenge.services || [] as service, serviceIndex}
							<div class="space-y-3 rounded-lg border border-stone-800 bg-stone-900/20 p-3 sm:p-4">
								<div class="grid gap-2 sm:grid-cols-[9rem_1fr_7rem_auto]"><input bind:value={service.name} required class="min-w-0 font-mono {fieldCls}" placeholder="app" /><input bind:value={service.image} required class="min-w-0 font-mono {fieldCls}" placeholder="ghcr.io/org/image" /><input bind:value={service.tag} class="min-w-0 font-mono {fieldCls}" placeholder="latest" /><button type="button" on:click={() => removeEditService(serviceIndex)} class="justify-self-start p-2 text-stone-600 hover:text-down sm:justify-self-auto"><Icon icon="mdi:trash-can-outline" class="h-4 w-4" /></button></div>
								<div class="grid gap-3 sm:grid-cols-2"><label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={service.public} class="accent-amber-500" /> Public route</label><label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={service.egress} class="accent-amber-500" /> Internet egress</label></div>
								<div class="grid gap-3 sm:grid-cols-2"><label><span class={labelCls}>CPU</span><input bind:value={service.cpu_limit} class="w-full {fieldCls}" placeholder="1" /></label><label><span class={labelCls}>Memory</span><input bind:value={service.memory_limit} class="w-full {fieldCls}" placeholder="512Mi" /></label></div>
								<div><div class="mb-2 flex items-center justify-between"><span class={labelCls}>Ports</span><button type="button" on:click={() => service.ports = [...(service.ports || []), { port: 0, protocol: 'tcp', service: 'tcp', internal: !service.public }]} class="text-xs text-stone-500 hover:text-stone-300">+ Port</button></div>{#each service.ports || [] as port, portIndex}<div class="mb-2 flex flex-wrap items-center gap-2"><input type="number" bind:value={port.port} min="1" max="65535" class="w-24 {fieldCls}" /><select bind:value={port.service} class="min-w-32 flex-1 {fieldCls}"><option value="http">HTTP</option><option value="tcp">TCP</option></select><label class="flex items-center gap-1 text-xs text-stone-500"><input type="checkbox" bind:checked={port.internal} class="accent-amber-500" /> Internal</label><button type="button" on:click={() => service.ports = service.ports.filter((_port: any, index: number) => index !== portIndex)} class="p-1 text-stone-600 hover:text-down"><Icon icon="mdi:close" class="h-4 w-4" /></button></div>{/each}</div>
								<div class="grid gap-3 sm:grid-cols-2"><label><span class={labelCls}>Command arguments</span><textarea bind:value={service.command_text} rows="3" class="w-full font-mono {fieldCls}" placeholder="One argument per line"></textarea></label><label><span class={labelCls}>Environment</span><textarea bind:value={service.env_text} rows="3" class="w-full font-mono {fieldCls}" placeholder="KEY=value"></textarea></label></div>
							</div>
						{/each}
						{#if !(editingChallenge.services || []).length}<button type="button" on:click={addEditService} class="w-full rounded-md border border-dashed border-stone-700 p-5 text-sm text-stone-500 hover:border-stone-600 hover:text-stone-300">Add the first service role</button>{/if}
						<label class="flex items-center gap-2 text-xs text-stone-400"><input type="checkbox" bind:checked={editingChallenge.privesc} class="accent-amber-500" /> SUID</label>
					</div>
				{/if}

				{#if editingChallenge.delivery_type === 'vm'}
					<div class="border border-stone-800 rounded-lg p-4 space-y-4">
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
						<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
							<label><span class={labelCls}>Run time (min)</span><input type="number" bind:value={editingChallenge.vm_timeout_minutes} min="1" class="w-full {fieldCls} tabular-nums" /></label>
							<label><span class={labelCls}>Max extensions</span><input type="number" bind:value={editingChallenge.vm_max_extensions} min="0" class="w-full {fieldCls} tabular-nums" /></label>
							<label><span class={labelCls}>Extension (min)</span><input type="number" bind:value={editingChallenge.vm_extension_minutes} min="1" class="w-full {fieldCls} tabular-nums" /></label>
						</div>
					</div>
				{/if}

				{#if editingChallenge.delivery_type !== 'static' && editingChallenge.delivery_type !== 'external'}
				<div class="border border-stone-800 rounded-lg p-4 space-y-3">
					<h3 class="metadata-label text-stone-400">Instance lifecycle</h3>
					<div class="grid gap-3 sm:grid-cols-3">
						<label class="block">
							<span class={labelCls}>{editingChallenge.delivery_type === 'vm' ? 'Compatibility timeout' : 'Timeout (min)'}</span>
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
				{/if}

				<div class="flex gap-3 pt-2">
					<button type="submit" disabled={actionLoading === editingChallenge.id} class="flex-1 {btnPrimary}">
							{actionLoading === editingChallenge.id ? 'Saving…' : 'Save'}
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
							<option value="static">Exact</option>
							<option value="regex">Pattern</option>
							<option value="dynamic">Per-instance</option>
									</select>
									{#if f.flag_type === 'dynamic'}
										<input type="text" bind:value={f.dynamic_flag_prefix} placeholder="Prefix e.g. CTF" class="flex-1 font-mono {fieldCls}" />
									{:else}
										<input type="text" bind:value={f.flag} placeholder={f.has_value ? '•••••••• (unchanged - type to replace)' : (f.flag_type === 'regex' ? 'regex pattern' : 'flag value')} class="flex-1 font-mono {fieldCls}" />
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
								<textarea bind:value={hnt.content} rows="2" placeholder="Hint" class="w-full {fieldCls} resize-none"></textarea>
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

			{#if editTab === 'grading'}
				<div class="p-6 space-y-5">
					{#if gradingError}<div class="px-3 py-2 rounded-md bg-down/10 border border-down/30 text-down text-xs">{gradingError}</div>{/if}
					{#if !grading}
						<p class="text-stone-500 text-sm">{gradingLoading ? 'Loading…' : ''}</p>
					{:else}
						{#if grading.scoring_mode !== 'graded'}
							<p class="px-3 py-2 rounded-md bg-warn/10 border border-warn/30 text-warn text-xs">Save the Settings tab to switch this challenge to graded; grader calls are refused until then.</p>
						{/if}
						<div>
							<span class={labelCls}>Challenge secret</span>
							<div class="flex items-center gap-2">
								<code class="flex-1 min-w-0 truncate px-3 py-2 bg-stone-950 border border-stone-800 rounded-md font-mono text-xs {secretShown ? 'text-amber-500' : 'text-stone-500'}">{secretShown ? grading.secret : '•'.repeat(32)}</code>
								<button type="button" on:click={() => (secretShown = !secretShown)} class="{btnGhost} px-3">{secretShown ? 'Hide' : 'Reveal'}</button>
								<button type="button" on:click={copySecret} class="{btnGhost} px-3">{secretCopied ? 'Copied' : 'Copy'}</button>
								<button type="button" on:click={rotateSecret} class="inline-flex items-center justify-center gap-2 px-3 py-2 rounded-md border border-down/30 bg-down/10 text-down text-sm leading-none font-medium hover:bg-down/20 transition-colors">Rotate</button>
							</div>
							<p class="text-stone-600 text-xs mt-1.5">Per-instance HMAC · internal roles only</p>
						</div>
						<div>
							<span class={labelCls}>Grader role env</span>
							<pre class="px-3 py-2 bg-stone-950 border border-stone-800 rounded-md font-mono text-xs text-stone-300 whitespace-pre-wrap">{graderEnv}</pre>
						</div>
						<div class="grid grid-cols-3 gap-3">
							{#each [{ l: 'Teams scoring', v: grading.scored_teams }, { l: 'Evaluations', v: grading.evaluations }, { l: 'Credits charged', v: Math.round(grading.credits_charged) }] as st}
								<div class="rounded-lg border border-stone-800 bg-stone-950 px-3 py-2">
									<p class="metadata-label text-stone-500 mb-1">{st.l}</p>
									<p class="text-base font-semibold text-stone-100 tabular-nums">{st.v}</p>
								</div>
							{/each}
						</div>
						<div>
							<div class="flex items-center justify-between mb-2">
								<span class="metadata-label text-stone-400">Recent evaluations</span>
								<button type="button" on:click={loadGrading} disabled={gradingLoading} class="text-xs text-stone-400 hover:text-stone-200 transition-colors inline-flex items-center gap-1 disabled:opacity-50"><Icon icon="mdi:refresh" class="w-3.5 h-3.5" /> Refresh</button>
							</div>
							{#if grading.log.length === 0}
								<p class="text-stone-600 text-sm">No evaluations yet.</p>
							{:else}
								<div class="overflow-x-auto border border-stone-800 rounded-lg">
									<table class="w-full min-w-[560px] text-xs">
										<thead>
											<tr class="metadata-label text-stone-500 border-b border-stone-800">
												<th class="px-3 py-2 text-left">When</th>
												<th class="px-3 py-2 text-left">Team</th>
												<th class="px-3 py-2 text-left">Eval</th>
												<th class="px-3 py-2 text-right">#</th>
												<th class="px-3 py-2 text-right">Charged</th>
												<th class="px-3 py-2 text-left">Status</th>
												<th class="px-3 py-2 text-right">Score</th>
											</tr>
										</thead>
										<tbody>
											{#each grading.log as e (e.eval_id)}
												<tr class="border-b border-stone-800/60 last:border-0">
													<td class="px-3 py-1.5 text-stone-500 tabular-nums whitespace-nowrap" title={instantTitle(e.created_at)}>{formatLocalDateTime(e.created_at)}</td>
													<td class="px-3 py-1.5 text-stone-200 truncate max-w-[10rem]">{e.team}</td>
													<td class="px-3 py-1.5 font-mono text-stone-400 truncate max-w-[8rem]" title={e.eval_id}>{e.eval_id}</td>
													<td class="px-3 py-1.5 text-right text-stone-400 tabular-nums">{e.seq}</td>
													<td class="px-3 py-1.5 text-right tabular-nums {e.charged > 0 ? 'text-amber-500' : 'text-stone-600'}">{e.charged > 0 ? e.charged : 'free'}</td>
													<td class="px-3 py-1.5"><span class="inline-flex px-1.5 py-0.5 rounded border text-[0.65rem] leading-none {evalStatusCls[e.status] ?? ''}"><span class="badge-label">{e.status.replace('_', ' ')}</span></span></td>
													<td class="px-3 py-1.5 text-right tabular-nums text-stone-200">{e.score == null ? '—' : e.score.toFixed(3)}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							{/if}
						</div>
					{/if}
				</div>
			{/if}

			{#if editTab === 'files'}
				<div class="p-4 sm:p-6 space-y-5">
					{#if subLoading}
						<p class="text-stone-500 text-sm">Loading…</p>
					{:else}
						<div>
							<div class="mb-2 flex items-center justify-between gap-3"><p class="text-sm font-medium text-stone-300">Handouts</p><label class="shrink-0 inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-stone-700 px-3 py-2 text-xs text-stone-300 hover:bg-stone-800/40"><Icon icon="mdi:upload" class="h-4 w-4" /> {subUploading ? 'Uploading…' : 'Upload'}<input type="file" multiple on:change={uploadEditAttachment} disabled={subUploading} class="hidden" /></label></div>
							<div class="space-y-2">
								{#each editAttachments as a (a.id)}
									<div class="flex items-start gap-3 rounded-lg border border-stone-800 p-3">
										<Icon icon={a.url ? 'mdi:link-variant' : 'mdi:file-outline'} class="mt-0.5 h-4 w-4 shrink-0 text-stone-500" />
										<div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-x-2 gap-y-1"><span class="truncate text-sm text-stone-200">{a.filename}</span><span class="text-[10px] uppercase tracking-wide text-stone-600">{a.url ? 'External' : 'Managed'} · {humanSize(a.file_size)}</span></div>{#if a.description}<p class="mt-1 text-xs text-stone-500">{a.description}</p>{/if}<p class="mt-1 truncate font-mono text-[10px] text-stone-700" title={a.sha256}>{a.sha256 ? `sha256:${a.sha256}` : 'Checksum not supplied'}{a.url ? ` · ${a.url}` : ''}</p></div>
										<button type="button" on:click={() => deleteEditAttachment(a)} title="Delete file" class="p-1.5 text-stone-600 hover:text-down transition-colors"><Icon icon="mdi:trash-can-outline" class="w-4 h-4" /></button>
									</div>
								{/each}
								{#if !editAttachments.length}<div class="rounded-lg border border-dashed border-stone-800 p-6 text-center text-sm text-stone-600">No handouts attached.</div>{/if}
							</div>
						</div>

						<form on:submit|preventDefault={addExternalHandout} class="space-y-3 rounded-lg border border-stone-800 bg-stone-900/20 p-4">
							<p class="text-sm font-medium text-stone-300">External handout</p>
							<div class="grid gap-3 sm:grid-cols-2"><label><span class={labelCls}>Filename</span><input bind:value={externalHandout.name} required maxlength="255" placeholder="challenge-files.zip" class="w-full {fieldCls}" /></label><label><span class={labelCls}>HTTPS URL</span><input type="url" bind:value={externalHandout.url} required placeholder="https://cdn.example.com/challenge-files.zip" class="w-full {fieldCls}" /></label></div>
							<div class="grid gap-3 sm:grid-cols-[1fr_auto]"><label><span class={labelCls}>SHA-256 <span class="normal-case tracking-normal text-stone-700">(recommended)</span></span><input bind:value={externalHandout.sha256} pattern="[A-Fa-f0-9]{64}" maxlength="64" placeholder="64 hexadecimal characters" class="w-full font-mono {fieldCls}" /></label><button type="submit" disabled={subUploading || !externalHandout.name.trim() || !externalHandout.url.trim()} class="self-end {btnPrimary}">Add link</button></div>
							<label><span class={labelCls}>Description</span><input bind:value={externalHandout.description} maxlength="1000" placeholder="Optional player-facing note" class="w-full {fieldCls}" /></label>
						</form>
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
