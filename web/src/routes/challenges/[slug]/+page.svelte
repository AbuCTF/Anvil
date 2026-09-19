<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$api';
	import { auth } from '$stores/auth';
	import { categoryColor, difficultyClass } from '$lib/rank';
	import { API_BASE } from '$lib/config';
	import Card from '$lib/components/Card.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { formatLocalDateTime, instantTitle } from '$lib/time';

	let challenge: any = null;
	let instance: any = null;
	let loading = true;
	let error = '';
	let actionError = '';
	let cooldownInfo: { until: number; remaining: number } | null = null;

	let flagInput = '';
	let submitting = false;
	let submitResult: { correct: boolean; message: string } | null = null;

	let creatingInstance = false;
	let instanceAction = '';
	let instanceError = '';
	let instanceLoadFailed = false;
	let instanceLoadedForToken = '';
	let timerInterval: ReturnType<typeof setInterval>;
	let timeRemaining = '';

	let isEditing = false;
	let editForm: { name: string; description: string; difficulty: string; base_points: number | string } | null = null;
	let saving = false;
	let showEditSuccess = false;

	let editingFlags: any[] = [];
	let showFlagModal = false;
	let newFlag = { name: '', flag: '', points: 100 };
	let savingFlag = false;

	let attachmentUploading = false;
	let attachmentUploadProgress = 0;
	let attachmentFileInput: HTMLInputElement;
	let attachmentDescription = '';
	// reactive flag: bind:this alone doesn't trigger re-evaluation when the file changes
	let attachmentFileSelected = false;

	function onAttachmentFileChange(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		attachmentFileSelected = !!input.files?.length;
	}

	async function uploadAttachment() {
		if (!attachmentFileInput?.files?.[0] || !challenge?.id) return;
		const file = attachmentFileInput.files[0];
		attachmentUploading = true;
		attachmentUploadProgress = 0;
		actionError = '';
		try {
			const fd = new FormData();
			fd.append('file', file);
			if (attachmentDescription) fd.append('description', attachmentDescription);
			await api.uploadAttachment(challenge.id, fd, (p) => { attachmentUploadProgress = p; });
			await loadChallenge();
			attachmentDescription = '';
			attachmentFileSelected = false;
			if (attachmentFileInput) attachmentFileInput.value = '';
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Upload failed';
		} finally {
			attachmentUploading = false;
		}
	}

	async function deleteAttachment(attachmentId: string) {
		if (!challenge?.id) return;
		if (!confirm('Delete this attachment?')) return;
		actionError = '';
		try {
			await api.deleteAttachment(challenge.id, attachmentId);
			await loadChallenge();
		} catch (e) {
			actionError = e instanceof Error ? e.message : 'Failed to delete attachment';
		}
	}

	function formatFileSize(bytes: number): string {
		if (bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
		return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
	}

	const slug = $page.params.slug;

	$: if (!slug) {
		error = 'Invalid challenge';
	}

	$: isAdmin = $auth.isAuthenticated && $auth.user?.role === 'admin';
	$: if ($auth.isAuthenticated && $auth.accessToken && instanceLoadedForToken !== $auth.accessToken) {
		instanceLoadedForToken = $auth.accessToken;
		loadUserInstance();
	}
	$: if (!$auth.isAuthenticated && !$auth.isLoading) {
		instanceLoadedForToken = '';
		instance = null;
	}

	// colored difficulty pill — shared with the challenge tiles. see DESIGN.md.
	$: diffClass = difficultyClass(challenge?.difficulty);

	// solver podium — rendered only if the API supplies ordered solve data. first
	// blood is the one sanctioned saturated pop (blood token).
	$: solvers = (() => {
		const raw = challenge?.solvers ?? challenge?.solves ?? challenge?.first_bloods ?? [];
		if (!Array.isArray(raw)) return [] as { rank: number; name: string; at: string | number | null }[];
		return raw.slice(0, 3).map((s: any, i: number) => ({
			rank: s?.rank ?? i + 1,
			name: s?.username ?? s?.name ?? s?.team_name ?? s?.display_name ?? 'Unknown',
			at: s?.solved_at ?? s?.created_at ?? s?.timestamp ?? null
		}));
	})();

	function formatSolvedAt(ts: string | number | null): string {
		if (!ts) return '';
		const formatted = formatLocalDateTime(ts, typeof ts === 'number' ? 'seconds' : 'auto');
		return formatted === '—' ? '' : formatted;
	}

	const podiumRank: Record<number, { cls: string; label: string }> = {
		1: { cls: 'text-blood bg-blood/10 border-blood/20', label: '1st' },
		2: { cls: 'text-stone-200 bg-stone-800/40 border-stone-700', label: '2nd' },
		3: { cls: 'text-amber-600/80 bg-stone-800/30 border-stone-800', label: '3rd' }
	};

	onMount(async () => {
		await loadChallenge();

		timerInterval = setInterval(() => {
			if (instance?.expires_at) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
				if (instance.expires_at < Math.floor(Date.now() / 1000)) {
					instance = null;
					loadUserInstance();
				}
			}
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
			challenge = await api.getChallenge(slug);
			editForm = {
				name: challenge.name,
				description: challenge.description || '',
				difficulty: challenge.difficulty,
				base_points: challenge.base_points
			};
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
			instanceError = '';
			instanceLoadFailed = false;
			if (instance) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
			}
		} catch (e) {
			instanceError = e instanceof Error ? e.message : 'Failed to load instance status';
			instanceLoadFailed = true;
		}
	}

	let ecoBusy = false;
	let ecoError = '';
	$: locked = challenge?.economy?.enabled && !challenge.economy.launched;

	async function ecoAction(fn: () => Promise<unknown>) {
		if (ecoBusy) return;
		ecoBusy = true;
		ecoError = '';
		try {
			await fn();
			await loadChallenge();
		} catch (e) {
			ecoError = e instanceof Error ? e.message : 'action failed';
		} finally {
			ecoBusy = false;
		}
	}

	const launchChallenge = () => ecoAction(() => api.openChallenge(slug!));
	const abandonChallenge = () => ecoAction(() => api.abandonChallenge(slug!));
	const extendTimer = () => ecoAction(() => api.extendChallenge(slug!));

	async function submitFlag() {
		if (!slug || !flagInput.trim()) return;
		submitting = true;
		submitResult = null;

		try {
			const result = await api.submitFlag(slug, flagInput.trim());
			submitResult = { correct: result.correct, message: result.message };
			if (result.correct) {
				flagInput = '';
				await Promise.all([loadChallenge(), auth.checkAuth(true)]);
			}
		} catch (e: unknown) {
			const msg = e instanceof Error ? e.message : 'Submission failed';
			submitResult = {
				correct: false,
				message: msg
			};
		} finally {
			submitting = false;
		}
	}

	async function startInstance() {
		if (!slug) return;
		creatingInstance = true;
		instanceError = '';
		try {
			const result = await api.createInstance(slug);
			instance = result.instance;
			if (instance) {
				timeRemaining = formatTimeRemaining(instance.expires_at);
			}
		} catch (e: any) {
			if (e?.cooldown_until) {
				cooldownInfo = {
					until: e.cooldown_until,
					remaining: e.remaining_seconds
				};
			}
			instanceError = e instanceof Error ? e.message : 'Failed to start instance';
		} finally {
			creatingInstance = false;
		}
	}

	async function extendInstance() {
		if (!instance) return;
		instanceAction = 'extending';
		instanceError = '';
		try {
			const result = await api.extendInstance(instance.id);
			instance = { ...instance, expires_at: result.new_expires_at, extensions_used: result.extensions_used };
			timeRemaining = formatTimeRemaining(result.new_expires_at);
		} catch (e) {
			instanceError = e instanceof Error ? e.message : 'Failed to extend';
		} finally {
			instanceAction = '';
		}
	}

	async function stopInstance() {
		if (!instance || !confirm('Stop this instance? You will have a cooldown period before starting again.')) return;
		instanceAction = 'stopping';
		instanceError = '';
		try {
			const result = await api.stopInstance(instance.id);
			instance = null;
			if (result.cooldown_until) {
				cooldownInfo = {
					until: result.cooldown_until,
					remaining: result.cooldown_minutes * 60
				};
			}
		} catch (e) {
			instanceError = e instanceof Error ? e.message : 'Failed to stop';
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
				base_points: parseInt(String(editForm.base_points))
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
		if (!Number.isFinite(expiresAt) || expiresAt <= 0) return '—';
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

	function getSecondsRemaining(expiresAt: number): number {
		if (!Number.isFinite(expiresAt) || expiresAt <= 0) return Number.POSITIVE_INFINITY;
		return expiresAt - Math.floor(Date.now() / 1000);
	}

	function getTimeColorClass(expiresAt: number): string {
		const seconds = getSecondsRemaining(expiresAt);
		if (seconds < 300) return 'text-down animate-pulse';
		if (seconds < 600) return 'text-warn';
		return 'text-stone-100';
	}

	let copiedKey = '';
	async function copyToClipboard(text: string, key = text) {
		try {
			await navigator.clipboard.writeText(text);
			copiedKey = key;
			setTimeout(() => {
				if (copiedKey === key) copiedKey = '';
			}, 1500);
		} catch (e) {
			instanceError = e instanceof Error ? e.message : 'Failed to copy to clipboard';
		}
	}

	function instanceProgress(inst: any): number {
		if (!inst?.expires_at || !inst?.created_at) return 100;
		const createdAt = typeof inst.created_at === 'number'
			? (inst.created_at > 1_000_000_000_000 ? inst.created_at / 1000 : inst.created_at)
			: Date.parse(inst.created_at) / 1000;
		if (!Number.isFinite(createdAt)) return 100;
		const total = inst.expires_at - createdAt;
		if (total <= 0) return 0;
		const remaining = inst.expires_at - Math.floor(Date.now() / 1000);
		return Math.max(0, Math.min(100, (remaining / total) * 100));
	}

	function getTimeBarClass(expiresAt: number): string {
		const s = getSecondsRemaining(expiresAt);
		if (s < 300) return 'bg-down';
		if (s < 600) return 'bg-warn';
		return 'bg-up';
	}

	// muted callout tokens — reserved semantic color only, no neon.
	const calloutStyles: Record<string, { label: string; cls: string; icon: string }> = {
		NOTE: { label: 'Note', cls: 'border-info/30 bg-info/10 text-info', icon: 'info' },
		TIP: { label: 'Tip', cls: 'border-up/30 bg-up/10 text-up', icon: 'info' },
		IMPORTANT: { label: 'Important', cls: 'border-amber-500/30 bg-amber-500/10 text-amber-500', icon: 'info' },
		WARNING: { label: 'Warning', cls: 'border-warn/30 bg-warn/10 text-warn', icon: 'warn' },
		CAUTION: { label: 'Caution', cls: 'border-down/30 bg-down/10 text-down', icon: 'warn' }
	};

	const calloutIcons: Record<string, string> = {
		info: '<svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>',
		warn: '<svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>'
	};

	function escapeHtml(s: string): string {
		return s
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;')
			.replace(/'/g, '&#39;');
	}

	function inlineMd(s: string): string {
		return escapeHtml(s)
			.replace(/`([^`]+)`/g, '<code class="px-1.5 py-0.5 rounded bg-stone-950 border border-stone-800 text-stone-200 text-[0.85em]">$1</code>')
			.replace(/\*\*([^*]+)\*\*/g, '<strong class="font-semibold text-stone-100">$1</strong>')
			.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer" class="text-amber-500 hover:text-amber-400 underline underline-offset-2">$1</a>');
	}

	function renderMarkdown(src: string): string {
		if (!src) return '';
		const lines = src.replace(/\r\n/g, '\n').split('\n');
		const blocks: string[] = [];
		let para: string[] = [];
		let i = 0;

		const flushPara = () => {
			if (para.length) {
				blocks.push(`<p class="leading-relaxed">${para.map(inlineMd).join('<br>')}</p>`);
				para = [];
			}
		};

		while (i < lines.length) {
			const line = lines[i];
			if (/^\s*>/.test(line)) {
				flushPara();
				const quote: string[] = [];
				while (i < lines.length && /^\s*>/.test(lines[i])) {
					quote.push(lines[i].replace(/^\s*>\s?/, ''));
					i++;
				}
				const m = quote[0]?.match(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(.*)$/i);
				if (m) {
					const st = calloutStyles[m[1].toUpperCase()];
					const body = [m[2], ...quote.slice(1)].filter((l) => l.trim() !== '');
					const inner = body.map(inlineMd).join('<br>');
					blocks.push(
						`<div class="rounded-lg border px-4 py-3 ${st.cls}"><div class="metadata-label flex items-center gap-1.5 mb-1">${calloutIcons[st.icon]}${st.label}</div><div class="text-sm text-stone-300 leading-relaxed">${inner}</div></div>`
					);
				} else {
					const inner = quote.map(inlineMd).join('<br>');
					blocks.push(`<blockquote class="border-l-2 border-stone-700 pl-3 text-stone-400 italic">${inner}</blockquote>`);
				}
				continue;
			}
			if (line.trim() === '') {
				flushPara();
				i++;
				continue;
			}
			para.push(line);
			i++;
		}
		flushPara();
		return blocks.join('');
	}

	async function saveFlag(flag: any) {
		if (!challenge) return;
		savingFlag = true;
		try {
			await api.updateFlag(challenge.id, flag.id, {
				name: flag.name,
				flag: flag.newFlag || undefined,
				points: flag.points,
				order: flag.order
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

<div class="min-h-screen bg-stone-950">
	{#if loading}
		<div class="flex items-center justify-center min-h-[60vh]">
			<Icon icon="mdi:loading" class="w-7 h-7 text-stone-600 animate-spin" />
		</div>
	{:else if error && !challenge}
		<div class="max-w-2xl mx-auto px-4 py-20 text-center">
			<Icon icon="mdi:alert-circle-outline" class="w-10 h-10 text-down/60 mx-auto mb-4" />
			<h2 class="text-base font-semibold text-stone-100 mb-2">Challenge not found</h2>
			<p class="text-stone-500 text-sm mb-6">{error}</p>
			<a href="/challenges" class="text-stone-400 hover:text-stone-200 text-sm transition-colors">
				← Back to challenges
			</a>
		</div>
	{:else if challenge}
		{#if showEditSuccess}
			<div class="fixed top-4 right-4 z-50 bg-up/10 border border-up/20 text-up px-4 py-2 rounded-lg text-sm leading-none flex items-center gap-2">
				<OpticalIcon icon="mdi:check" size={14} box={14} />
				<span class="optical-label">Saved</span>
			</div>
		{/if}

		<div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
			<a href="/challenges" class="inline-flex items-center gap-1.5 text-stone-500 hover:text-stone-300 text-sm leading-none mb-8 transition-colors">
				<OpticalIcon icon="mdi:arrow-left" size={14} box={14} />
				<span class="optical-label">Challenges</span>
			</a>

			{#if actionError}
				<div class="mb-6 flex items-start justify-between gap-3 rounded-lg border border-down/20 bg-down/10 px-4 py-3 text-sm text-down" aria-live="polite">
					<span>{actionError}</span>
					<button type="button" on:click={() => actionError = ''} class="shrink-0 text-stone-500 hover:text-stone-200" aria-label="Dismiss error">
						<Icon icon="mdi:close" class="w-4 h-4" />
					</button>
				</div>
			{/if}

			<div class="detail-in grid grid-cols-1 lg:grid-cols-3 gap-8">
				<div class="lg:col-span-2 space-y-6">
					<div class="flex items-start justify-between gap-4 pb-6 border-b border-stone-800">
						<div class="flex-1 min-w-0">
							{#if isEditing && editForm}
								<input
									type="text"
									bind:value={editForm.name}
									class="text-2xl font-semibold bg-transparent border-b border-stone-700 text-stone-100 w-full focus:outline-none focus:border-stone-500 pb-1"
								/>
							{:else}
								<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">{challenge.name}</h1>
							{/if}

							<div class="flex flex-wrap items-center gap-2.5 mt-3 leading-none">
								{#if isEditing && editForm}
									<select bind:value={editForm.difficulty} class="text-xs px-2 py-1 rounded bg-stone-900 border border-stone-700 text-stone-300 focus:outline-none">
										<option value="easy">Easy</option>
										<option value="medium">Medium</option>
										<option value="hard">Hard</option>
										<option value="insane">Insane</option>
									</select>
								{:else}
									<span class="text-[0.68rem] font-medium px-2 py-0.5 rounded-full border capitalize {diffClass}">
										<span class="badge-label">{challenge.difficulty}</span>
									</span>
								{/if}

								{#if challenge.has_instance}
									<span class="inline-flex items-center gap-1.5 text-xs leading-none text-stone-500">
										<OpticalIcon icon={challenge.resource_type === 'vm' ? 'mdi:desktop-classic' : 'mdi:docker'} size={12} box={12} />
										<span class="optical-label">{challenge.resource_type === 'vm' ? 'VM' : 'Docker'}</span>
									</span>
								{/if}

								{#if challenge.category}
									<span class="inline-flex items-center gap-1.5 text-xs leading-none text-stone-500">
										<span class="w-2 h-2 rounded-full shrink-0" style="background:{categoryColor(challenge.category)}"></span>
										<span class="optical-label">{challenge.category}</span>
									</span>
								{/if}

								{#if challenge.status === 'draft'}
									<span class="text-[0.68rem] font-medium px-2 py-0.5 rounded-full bg-warn/10 border border-warn/20 text-warn">
										<span class="badge-label">Draft</span>
									</span>
								{/if}

								{#if challenge.is_solved}
									<span class="text-[0.68rem] leading-none font-medium px-2 py-0.5 rounded-full bg-up/10 border border-up/20 text-up flex items-center gap-1">
										<OpticalIcon icon="mdi:check" size={12} box={12} />
										<span class="badge-label">Solved</span>
									</span>
								{/if}
							</div>
						</div>

						<div class="text-right shrink-0">
							{#if isEditing && editForm}
								<input
									type="number"
									bind:value={editForm.base_points}
									class="w-16 text-xl font-semibold bg-transparent border-b border-stone-700 text-stone-100 text-right focus:outline-none focus:border-stone-500 tabular-nums"
								/>
								<p class="metadata-label text-stone-500 mt-1">Points</p>
							{:else}
								<p class="text-2xl font-semibold text-amber-500 tabular-nums">{challenge.base_points}</p>
								<p class="metadata-label text-stone-500">Points</p>
							{/if}
						</div>
					</div>

					{#if isAdmin}
						<div class="flex items-center gap-2 pb-4 border-b border-stone-800/60 leading-none">
							{#if isEditing}
								<button on:click={handleSaveEdit} disabled={saving} class="text-xs px-3 py-1.5 bg-stone-100 text-stone-950 rounded-md font-medium hover:bg-stone-50 disabled:opacity-50 transition-colors flex items-center gap-1.5">
									{#if saving}<Icon icon="mdi:loading" class="w-3 h-3 animate-spin" />{/if}
									Save
								</button>
								<button on:click={cancelEdit} class="text-xs px-3 py-1.5 text-stone-400 hover:text-stone-100 transition-colors">
									Cancel
								</button>
							{:else}
								<button on:click={() => isEditing = true} class="text-xs px-3 py-1.5 text-stone-400 hover:text-stone-100 transition-colors flex items-center gap-1.5">
									<Icon icon="mdi:pencil" class="w-3 h-3" />
									Edit
								</button>
								{#if challenge.status === 'draft'}
									<button on:click={handlePublish} disabled={saving} class="text-xs px-3 py-1.5 text-up hover:text-up/80 transition-colors flex items-center gap-1.5 disabled:opacity-50">
										<Icon icon="mdi:eye" class="w-3 h-3" />
										Publish
									</button>
								{:else}
									<button on:click={handleUnpublish} disabled={saving} class="text-xs px-3 py-1.5 text-warn hover:text-warn/80 transition-colors flex items-center gap-1.5 disabled:opacity-50">
										<Icon icon="mdi:eye-off" class="w-3 h-3" />
										Unpublish
									</button>
								{/if}
								<a href="/admin" class="text-xs px-3 py-1.5 text-stone-500 hover:text-stone-300 transition-colors ml-auto">
									Admin Panel →
								</a>
							{/if}
						</div>
					{/if}

					{#if locked}
						<Card title="Locked">
							<div class="space-y-3">
								<p class="text-sm text-stone-400 leading-relaxed">Open this challenge to reveal the brief, download the files, and start solving.</p>
								<button on:click={launchChallenge} disabled={ecoBusy || challenge.economy.credits < challenge.economy.launch_cost} class="w-full py-2.5 bg-amber-500 text-amber-950 text-sm font-medium rounded-md hover:bg-amber-400 transition-colors disabled:opacity-50">
									{ecoBusy ? 'Opening…' : `Open for ${challenge.economy.launch_cost} credits`}
								</button>
								<p class="text-xs text-stone-600">balance: {Math.round(challenge.economy.credits)} credits</p>
								{#if ecoError}<p class="text-xs text-down">{ecoError}</p>{/if}
							</div>
						</Card>
					{:else}
						<Card title="Description">
							{#if isEditing && editForm}
								<textarea
									bind:value={editForm.description}
									rows="6"
									class="w-full px-3 py-2.5 bg-stone-950 border border-stone-800 rounded-md text-stone-300 text-sm leading-relaxed focus:outline-none focus:border-stone-600 resize-none"
									placeholder="Challenge description…"
								></textarea>
							{:else if challenge.description}
								<div class="font-sans text-sm text-stone-300 leading-relaxed space-y-3">
									<!-- eslint-disable-next-line svelte/no-at-html-tags -- renderMarkdown escapes source text before adding its fixed markup -->
									{@html renderMarkdown(challenge.description)}
								</div>
							{:else}
								<p class="text-stone-600 text-sm">No description provided.</p>
							{/if}
						</Card>
					{/if}

					{#if !locked && ((challenge.flags && challenge.flags.length > 0) || (isEditing && isAdmin))}
						<Card title="Objectives">
							<svelte:fragment slot="meta">
								{#if isEditing && isAdmin}
									<button
										on:click={() => showFlagModal = true}
									class="text-xs leading-none text-up hover:text-up/80 transition-colors flex items-center gap-1"
									>
									<Icon icon="mdi:plus" class="w-3 h-3 shrink-0" />
										Add Flag
									</button>
								{:else}
									<span class="text-xs text-stone-500 tabular-nums">{challenge.user_solves || 0}/{challenge.total_flags}</span>
								{/if}
							</svelte:fragment>

							<div class="space-y-2">
								{#if isEditing && isAdmin}
									{#each editingFlags as flag, i}
										<div class="py-3 px-4 bg-stone-950 border border-stone-800 rounded-lg">
											{#if flag.editing}
												<div class="space-y-3">
													<div class="grid grid-cols-2 gap-3">
														<input
															type="text"
															bind:value={flag.name}
															placeholder="Flag name"
															class="px-3 py-2 bg-stone-900 border border-stone-700 rounded-md text-sm text-stone-200 focus:outline-none focus:border-stone-600"
														/>
														<input
															type="number"
															bind:value={flag.points}
															placeholder="Points"
															class="px-3 py-2 bg-stone-900 border border-stone-700 rounded-md text-sm text-stone-200 focus:outline-none focus:border-stone-600 tabular-nums"
														/>
													</div>
													<input
														type="text"
														bind:value={flag.newFlag}
														placeholder="New flag value (leave empty to keep current)"
														class="w-full px-3 py-2 bg-stone-900 border border-stone-700 rounded-md text-sm text-stone-200 focus:outline-none focus:border-stone-600 font-mono"
													/>
													<div class="flex items-center gap-2 pt-1">
														<button
															on:click={() => { saveFlag(flag); flag.editing = false; }}
															disabled={savingFlag}
															class="px-3 py-1.5 bg-stone-100 hover:bg-stone-50 text-stone-950 text-xs font-medium rounded-md transition-colors disabled:opacity-50"
														>
															{savingFlag ? 'Saving…' : 'Save'}
														</button>
														<button
															on:click={() => { flag.editing = false; }}
															class="px-3 py-1.5 bg-stone-800 hover:bg-stone-700 text-stone-300 text-xs rounded-md transition-colors"
														>
															Cancel
														</button>
														<button
															on:click={() => deleteFlag(flag.id)}
															disabled={savingFlag}
															class="ml-auto px-3 py-1.5 text-down hover:text-down/80 text-xs transition-colors disabled:opacity-50"
														>
															Delete
														</button>
													</div>
												</div>
											{:else}
												<div class="flex items-center justify-between">
										<div class="flex items-center gap-3 leading-none">
														<div class="w-6 h-6 rounded flex items-center justify-center bg-stone-800 text-stone-500 text-xs font-medium tabular-nums">
															{i + 1}
														</div>
														<span class="text-sm text-stone-300">{flag.name}</span>
													</div>
													<div class="flex items-center gap-3">
														<span class="text-xs text-stone-500 tabular-nums">{flag.points} pts</span>
														<button
															on:click={() => { flag.editing = true; flag.newFlag = ''; }}
															class="text-xs text-stone-400 hover:text-stone-200 transition-colors"
															aria-label="Edit flag"
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
									{#each challenge.flags as flag, i}
										<div class="flex items-center justify-between py-3 px-4 rounded-lg {flag.is_solved ? 'bg-up/[0.06] border border-up/20' : 'bg-stone-950 border border-stone-800'}">
											<div class="flex items-center gap-3">
												<div class="w-6 h-6 rounded flex items-center justify-center {flag.is_solved ? 'bg-up/20 text-up' : 'bg-stone-800 text-stone-500'} text-xs font-medium tabular-nums">
													{#if flag.is_solved}
														<Icon icon="mdi:check" class="w-3.5 h-3.5" />
													{:else}
														{i + 1}
													{/if}
												</div>
												<span class="text-sm {flag.is_solved ? 'text-up' : 'text-stone-300'}">{flag.name}</span>
											</div>
											<div class="flex items-center gap-3">
												{#if typeof flag.total_solves === 'number'}
													<span class="text-xs leading-none text-stone-500 inline-flex items-center gap-1 tabular-nums">
														<OpticalIcon icon="mdi:account-group" size={12} box={12} />
														<span class="optical-label">{flag.total_solves}</span>
													</span>
												{/if}
												<span class="text-xs {flag.is_solved ? 'text-up/70' : 'text-stone-500'} tabular-nums">{flag.points} pts</span>
											</div>
										</div>
									{/each}
								{/if}
							</div>
						</Card>
					{/if}

					{#if challenge.hints && challenge.hints.length > 0}
						<Card title="Hints">
							<div class="space-y-2">
								{#each challenge.hints as hint, i}
									<div class="py-3 px-4 bg-stone-950 border border-stone-800 rounded-lg">
										{#if hint.is_unlocked}
											<p class="text-stone-400 text-sm">{hint.content}</p>
										{:else}
											<div class="flex items-center justify-between">
												<span class="text-stone-500 text-sm tabular-nums">Hint #{i + 1}</span>
										<button disabled title="Hint unlocking is temporarily unavailable" class="text-xs text-stone-600 cursor-not-allowed tabular-nums">
											Unlock unavailable
												</button>
											</div>
										{/if}
									</div>
								{/each}
							</div>
						</Card>
					{/if}

					{#if !locked && ((challenge.attachments && challenge.attachments.length > 0) || (isEditing && isAdmin))}
						<Card title="Files">
							<div class="space-y-2">
								{#each challenge.attachments as attachment}
									<div class="flex items-center justify-between py-2.5 px-4 bg-stone-950 border border-stone-800 rounded-lg group">
										<div class="flex items-center gap-3 min-w-0">
											<Icon icon="mdi:file-outline" class="w-4 h-4 text-stone-500 shrink-0" />
											<div class="min-w-0">
												<p class="text-sm text-stone-300 truncate">{attachment.filename}</p>
												<p class="text-xs text-stone-600 tabular-nums">{formatFileSize(attachment.file_size)}</p>
											</div>
										</div>
										<div class="flex items-center gap-2 shrink-0">
											<a
											href={`${API_BASE}/api/v1/challenges/${challenge.slug}/attachments/${attachment.id}/download`}
												download={attachment.filename}
											class="text-xs leading-none px-2.5 py-1 bg-stone-800 hover:bg-stone-700 text-stone-300 hover:text-stone-100 rounded-md transition-colors flex items-center gap-1"
											>
											<Icon icon="mdi:download" class="w-3 h-3 shrink-0" />
												Download
											</a>
											{#if isEditing && isAdmin}
												<button
													on:click={() => deleteAttachment(attachment.id)}
													class="text-xs text-down hover:text-down/80 transition-opacity opacity-0 group-hover:opacity-100"
													aria-label="Delete attachment"
												>
													<Icon icon="mdi:trash-can-outline" class="w-4 h-4" />
												</button>
											{/if}
										</div>
									</div>
								{/each}

								{#if isEditing && isAdmin}
									<div class="py-3 px-4 bg-stone-950 border border-dashed border-stone-700 rounded-lg space-y-3">
										<p class="metadata-label text-stone-500">Upload file</p>
										<input
											type="file"
											bind:this={attachmentFileInput}
											on:change={onAttachmentFileChange}
											class="block w-full text-xs text-stone-400 file:mr-3 file:py-1 file:px-3 file:rounded file:border-0 file:text-xs file:bg-stone-800 file:text-stone-300 hover:file:bg-stone-700 cursor-pointer"
										/>
										<input
											type="text"
											bind:value={attachmentDescription}
											placeholder="Description (optional)"
											class="block w-full px-3 py-1.5 bg-stone-950 border border-stone-800 rounded-md text-xs text-stone-100 placeholder-stone-600 focus:outline-none focus:border-stone-600"
										/>
										{#if attachmentUploading}
											<div class="space-y-1">
												<div class="h-1 bg-stone-800 rounded-full overflow-hidden">
													<div class="h-full bg-stone-400 rounded-full transition-all" style="width: {attachmentUploadProgress}%"></div>
												</div>
												<p class="text-xs text-stone-500 text-right tabular-nums">{attachmentUploadProgress}%</p>
											</div>
										{/if}
										<button
											on:click={uploadAttachment}
											disabled={attachmentUploading || !attachmentFileSelected}
											class="text-xs px-3 py-1.5 bg-stone-100 text-stone-950 font-medium rounded-md hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
										>
											{attachmentUploading ? 'Uploading…' : 'Upload'}
										</button>
									</div>
								{/if}
							</div>
						</Card>
					{/if}
				</div>

				<div class="space-y-6">
					{#if $auth.isAuthenticated && !locked && challenge.has_instance}
						<Card title="Instance">
							{#if instanceError}
								<div class="mb-4 flex items-start justify-between gap-3 rounded-md border border-down/20 bg-down/10 px-3 py-2 text-xs text-down" aria-live="polite">
									<span>{instanceError}</span>
									{#if instanceLoadFailed}
										<button type="button" on:click={loadUserInstance} class="shrink-0 text-stone-300 hover:text-stone-100">Retry</button>
									{:else}
										<button type="button" on:click={() => instanceError = ''} class="shrink-0 text-stone-500 hover:text-stone-200" aria-label="Dismiss instance error">
											<Icon icon="mdi:close" class="w-3.5 h-3.5" />
										</button>
									{/if}
								</div>
							{/if}
							{#if instance}
								<div class="space-y-4">
									<div>
										<div class="flex items-center justify-between mb-2">
											<div class="flex items-center gap-2 leading-none">
												<span class="w-2 h-2 bg-up rounded-full animate-pulse"></span>
												<span class="optical-label text-up text-sm font-medium">Running</span>
											</div>
											<span class="metadata-label text-stone-600">Session</span>
										</div>
										<div class="h-1.5 bg-stone-950 border border-stone-800 rounded-full overflow-hidden">
											<div
												class="h-full {getTimeBarClass(instance.expires_at)} rounded-full transition-all duration-1000 ease-linear"
												style="width: {instanceProgress(instance)}%"
											></div>
										</div>
									</div>

									<div>
										<p class="metadata-label text-stone-500 mb-2">Connect</p>
										{#if instance.ports && Object.keys(instance.ports).length > 0}
											<div class="space-y-2">
												{#each Object.entries(instance.ports) as [portKey]}
													{@const [port, svc] = portKey.split('/')}
													{@const isHttp = svc === 'http' || svc === 'https'}
													{@const connStr = isHttp ? `${svc}://${instance.ip_address}:${port}` : `nc ${instance.ip_address} ${port}`}
													<div class="bg-stone-950 border border-stone-800 rounded-lg overflow-hidden">
														<div class="flex items-center gap-2 px-3 py-1.5 border-b border-stone-800/60 bg-stone-900/40">
															<OpticalIcon
																icon={isHttp ? 'mdi:web' : 'mdi:console'}
																size={13.5}
																box={14}
																className={isHttp ? 'text-info' : 'text-stone-400'}
															/>
															<span class="optical-label metadata-label {isHttp ? 'text-info' : 'text-stone-400'}">
																{isHttp ? svc.toUpperCase() : 'TCP'}
															</span>
															<span class="text-xs text-stone-600 ml-auto tabular-nums">:{port}</span>
														</div>
														<div class="flex items-center justify-between px-3 py-2">
															{#if isHttp}
																<a href={connStr} target="_blank" rel="noopener" class="text-xs text-info hover:text-info/80 font-mono truncate flex-1 min-w-0">
																	{connStr}
																</a>
															{:else}
																<code class="text-xs text-stone-300 font-mono">{connStr}</code>
															{/if}
															<button
																on:click={() => copyToClipboard(connStr)}
																class="ml-2 flex-shrink-0 transition-colors {copiedKey === connStr ? 'text-up' : 'text-stone-600 hover:text-stone-300'}"
																aria-label="Copy connection"
															>
																<Icon icon={copiedKey === connStr ? 'mdi:check' : 'mdi:content-copy'} class="w-3.5 h-3.5" />
															</button>
														</div>
													</div>
												{/each}
											</div>
										{:else}
											<div class="bg-stone-950 border border-stone-800 rounded-lg px-3 py-2 flex items-center justify-between">
												<code class="text-xs text-stone-300 font-mono">{instance.ip_address}</code>
												<button on:click={() => copyToClipboard(instance.ip_address)} class="ml-2 transition-colors {copiedKey === instance.ip_address ? 'text-up' : 'text-stone-600 hover:text-stone-300'}" aria-label="Copy address">
													<Icon icon={copiedKey === instance.ip_address ? 'mdi:check' : 'mdi:content-copy'} class="w-3.5 h-3.5" />
												</button>
											</div>
										{/if}
									</div>

									<div class="grid grid-cols-2 gap-3">
										<div class="bg-stone-950 border border-stone-800 rounded-lg p-3">
											<p class="metadata-label text-stone-500 mb-1">Time left</p>
											<p
												class="text-base font-mono font-medium tabular-nums {getTimeColorClass(instance.expires_at)}"
												title={instantTitle(instance.expires_at, 'seconds')}
											>{timeRemaining}</p>
											{#if getSecondsRemaining(instance.expires_at) < 300}
												<p class="text-xs text-down mt-1">Expiring soon</p>
											{/if}
										</div>
										<div class="bg-stone-950 border border-stone-800 rounded-lg p-3">
											<p class="metadata-label text-stone-500 mb-1">Extensions</p>
											<p class="text-base font-medium text-stone-200 tabular-nums">{instance.extensions_used || 0}<span class="text-stone-600 font-normal text-sm"> / {instance.max_extensions || 3}</span></p>
										</div>
									</div>

									<div class="flex gap-2">
										<button on:click={extendInstance} disabled={instanceAction === 'extending' || (instance.extensions_used >= (instance.max_extensions || 3))} class="flex-1 text-xs leading-none py-2 bg-stone-900 text-stone-300 rounded-md border border-stone-800 hover:bg-stone-800/60 hover:border-stone-700 transition-colors disabled:opacity-40 disabled:cursor-not-allowed flex items-center justify-center gap-1.5">
											{#if instanceAction === 'extending'}
												<Icon icon="mdi:loading" class="w-3 h-3 shrink-0 animate-spin" />
											{:else}
												<Icon icon="mdi:clock-plus-outline" class="w-3 h-3 shrink-0" />
											{/if}
											{instanceAction === 'extending' ? 'Extending…' : 'Extend'}
										</button>
										<button on:click={stopInstance} disabled={instanceAction === 'stopping'} class="flex-1 text-xs leading-none py-2 bg-down/10 text-down rounded-md border border-down/30 hover:bg-down/20 transition-colors disabled:opacity-40 flex items-center justify-center gap-1.5">
											{#if instanceAction === 'stopping'}
												<Icon icon="mdi:loading" class="w-3 h-3 shrink-0 animate-spin" />
											{:else}
												<Icon icon="mdi:stop-circle-outline" class="w-3 h-3 shrink-0" />
											{/if}
											{instanceAction === 'stopping' ? 'Stopping…' : 'Stop'}
										</button>
									</div>
								</div>
							{:else if cooldownInfo}
								<div class="text-center py-4">
									<div class="w-11 h-11 rounded-full bg-warn/10 flex items-center justify-center mx-auto mb-3">
										<Icon icon="mdi:timer-sand" class="w-5 h-5 text-warn" />
									</div>
									<p class="text-warn text-sm font-medium mb-1">Cooldown active</p>
									<p class="text-2xl font-mono text-warn mb-2 tabular-nums">{formatCooldown(cooldownInfo.remaining)}</p>
									<p class="text-stone-500 text-xs">You can start a new instance after the cooldown period.</p>
								</div>
							{:else}
								<div class="text-center py-4">
									<p class="text-stone-500 text-sm mb-4">No active instance</p>
									<button on:click={startInstance} disabled={creatingInstance || instanceLoadFailed} class="w-full py-2.5 bg-stone-100 text-stone-950 text-sm leading-none font-medium rounded-md hover:bg-stone-50 transition-colors disabled:opacity-50 flex items-center justify-center gap-2">
										{#if creatingInstance}
											<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
											Starting…
										{:else}
											<Icon icon="mdi:play" class="w-3.5 h-3.5 shrink-0" />
											Start Instance
										{/if}
									</button>
								</div>
							{/if}
						</Card>
					{/if}

					{#if challenge?.economy?.launched && !challenge.economy.solved}
						<Card title="Launched">
							<div class="flex items-center justify-between">
								<span class="text-sm text-stone-400"><span class="text-amber-500 font-semibold tabular-nums">{Math.round(challenge.economy.credits)}</span> credits left</span>
								<div class="flex gap-2">
									<button on:click={extendTimer} disabled={ecoBusy} class="text-xs py-1.5 px-3 rounded-md border border-stone-800 text-stone-300 hover:bg-stone-800/40 disabled:opacity-40 transition-colors">Extend</button>
									<button on:click={abandonChallenge} disabled={ecoBusy} class="text-xs py-1.5 px-3 rounded-md border border-down/30 bg-down/10 text-down hover:bg-down/20 disabled:opacity-40 transition-colors">Abandon</button>
								</div>
							</div>
							{#if ecoError}<p class="mt-2 text-xs text-down">{ecoError}</p>{/if}
						</Card>
					{/if}

					{#if $auth.isAuthenticated && !locked}
						<Card title="Submit Flag">
							<form on:submit|preventDefault={submitFlag} class="space-y-3">
								<input
									type="text"
									bind:value={flagInput}
									placeholder="flag&#123;...&#125;"
									class="w-full px-3 py-2.5 bg-stone-950 border border-stone-800 rounded-md text-stone-100 text-sm font-mono placeholder-stone-600 focus:outline-none focus:border-stone-600"
								/>
								<button type="submit" disabled={submitting || !flagInput.trim()} class="w-full py-2.5 bg-stone-100 text-stone-950 text-sm font-medium rounded-md hover:bg-stone-50 transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
									{submitting ? 'Checking…' : 'Submit'}
								</button>
							</form>

							{#if submitResult}
								<div class="mt-3 flex items-start gap-2 py-2.5 px-3 rounded-lg text-sm border {submitResult.correct ? 'bg-up/10 border-up/20 text-up' : 'bg-down/10 border-down/20 text-down'}">
									<Icon icon={submitResult.correct ? 'mdi:check-circle' : 'mdi:alert-circle'} class="w-4 h-4 mt-0.5 shrink-0" />
									<span>{submitResult.message}</span>
								</div>
							{/if}
						</Card>
					{:else if !$auth.isAuthenticated}
						<Card hasHeader={false} bodyClass="p-6">
							<div class="text-center">
								<p class="text-stone-500 text-sm mb-4">Login to start this challenge</p>
								<a href="/login" class="inline-block w-full py-2.5 bg-stone-100 text-stone-950 text-sm font-medium rounded-md hover:bg-stone-50 transition-colors">
									Login
								</a>
							</div>
						</Card>
					{/if}

					<Card title="Solves">
						<svelte:fragment slot="meta">
							<span class="text-xs text-stone-500 tabular-nums">{challenge.total_solves}</span>
						</svelte:fragment>

						<div class="space-y-3">
							{#if solvers.length > 0}
								<ol class="space-y-2">
									{#each solvers as s (s.rank)}
										{@const rk = podiumRank[s.rank] ?? { cls: 'text-stone-300 bg-stone-800/30 border-stone-800', label: `${s.rank}` }}
										<li class="flex items-center justify-between py-2 px-3 rounded-lg border {rk.cls}">
											<div class="flex items-center gap-2.5 min-w-0 leading-none">
												<span class="w-6 shrink-0 inline-flex items-center justify-center">
													{#if s.rank === 1}
														<OpticalIcon icon="mdi:water" size={14} box={14} />
													{:else}
														<span class="optical-label text-[0.65rem] font-medium uppercase tracking-wide tabular-nums">{rk.label}</span>
													{/if}
												</span>
												<span class="optical-label text-sm truncate {s.rank === 1 ? 'font-medium' : 'text-stone-200'}">{s.name}</span>
											</div>
											{#if formatSolvedAt(s.at)}
												<span
													class="text-[0.68rem] text-stone-500 tabular-nums shrink-0 ml-2"
													title={instantTitle(s.at, typeof s.at === 'number' ? 'seconds' : 'auto')}
												>{formatSolvedAt(s.at)}</span>
											{/if}
										</li>
									{/each}
								</ol>
								<div class="flex items-center justify-between pt-1 text-sm border-t border-stone-800/60">
									<span class="metadata-label text-stone-500">Total solves</span>
									<span class="text-stone-200 tabular-nums font-medium">{challenge.total_solves}</span>
								</div>
							{:else if challenge.total_solves === 0}
								<div class="flex items-center gap-2 py-2 px-3 rounded-lg bg-blood/10 border border-blood/20 text-blood text-xs leading-none">
									<OpticalIcon icon="mdi:water" size={12} box={12} />
									<span class="optical-label">Unsolved — first blood available</span>
								</div>
							{:else}
								<div class="flex items-center justify-between py-2 px-3 rounded-lg bg-stone-950 border border-stone-800">
									<span class="text-stone-500 leading-none flex items-center gap-1.5">
										<OpticalIcon icon="mdi:account-group" size={13.5} box={14} />
										<span class="optical-label metadata-label">Solves</span>
									</span>
									<span class="text-lg font-semibold text-stone-100 tabular-nums">{challenge.total_solves}</span>
								</div>
							{/if}
						</div>
					</Card>

					<Card title="Details">
						<div class="space-y-2.5 text-sm">
							<div class="flex items-center justify-between">
								<span class="metadata-label text-stone-500">Flags</span>
								<span class="text-stone-300 tabular-nums">{challenge.total_flags}</span>
							</div>
							{#if challenge.has_instance}
								<div class="flex items-center justify-between">
									<span class="metadata-label text-stone-500">Type</span>
									<span class="text-stone-300">{challenge.resource_type === 'vm' ? 'Virtual Machine' : 'Docker'}</span>
								</div>
							{/if}
							{#if challenge.category}
								<div class="flex items-center justify-between">
									<span class="metadata-label text-stone-500">Category</span>
									<span class="text-stone-300 inline-flex items-center gap-1.5 leading-none">
										<span class="w-1.5 h-1.5 rounded-full shrink-0" style="background:{categoryColor(challenge.category)}"></span>
										<span class="optical-label">{challenge.category}</span>
									</span>
								</div>
							{/if}
							{#if challenge.author_name}
								<div class="flex items-center justify-between">
									<span class="metadata-label text-stone-500">Author</span>
									<span class="text-stone-300">{challenge.author_name}</span>
								</div>
							{/if}
						</div>
					</Card>
				</div>
			</div>
		</div>
	{/if}

	<style>
		@media (prefers-reduced-motion: no-preference) {
			.detail-in {
				animation: detailIn 0.3s ease both;
			}
		}
		@keyframes detailIn {
			from {
				opacity: 0;
				transform: translateY(8px);
			}
			to {
				opacity: 1;
				transform: translateY(0);
			}
		}
	</style>
</div>

{#if showFlagModal}
	<div class="fixed inset-0 bg-stone-950/80 flex items-center justify-center z-50 p-4">
		<div class="bg-stone-900 border border-stone-800 rounded-lg w-full max-w-md">
			<div class="px-4 py-3 border-b border-stone-800 flex items-center justify-between">
				<h3 class="text-sm font-semibold text-stone-200">Add New Flag</h3>
				<button on:click={() => showFlagModal = false} class="text-stone-500 hover:text-stone-300 transition-colors" aria-label="Close">
					<Icon icon="mdi:close" class="w-5 h-5" />
				</button>
			</div>
			<form on:submit|preventDefault={createNewFlag} class="p-4 space-y-4">
				<div>
					<label for="new-flag-name" class="metadata-label block text-stone-500 mb-1.5">Name</label>
					<input
						id="new-flag-name"
						type="text"
						bind:value={newFlag.name}
						placeholder="e.g., User Flag"
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded-md text-sm text-stone-200 focus:outline-none focus:border-stone-600"
					/>
				</div>
				<div>
					<label for="new-flag-value" class="metadata-label block text-stone-500 mb-1.5">Flag Value</label>
					<input
						id="new-flag-value"
						type="text"
						bind:value={newFlag.flag}
						placeholder="flag&#123;...&#125;"
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded-md text-sm text-stone-200 font-mono focus:outline-none focus:border-stone-600"
					/>
				</div>
				<div>
					<label for="new-flag-points" class="metadata-label block text-stone-500 mb-1.5">Points</label>
					<input
						id="new-flag-points"
						type="number"
						bind:value={newFlag.points}
						required
						class="w-full px-3 py-2 bg-stone-950 border border-stone-700 rounded-md text-sm text-stone-200 focus:outline-none focus:border-stone-600 tabular-nums"
					/>
				</div>
				<div class="flex items-center gap-3 pt-2">
					<button
						type="submit"
						disabled={savingFlag}
						class="flex-1 py-2 bg-stone-100 hover:bg-stone-50 text-stone-950 text-sm font-medium rounded-md transition-colors disabled:opacity-50"
					>
						{savingFlag ? 'Creating…' : 'Create Flag'}
					</button>
					<button
						type="button"
						on:click={() => showFlagModal = false}
						class="flex-1 py-2 bg-stone-800 hover:bg-stone-700 text-stone-300 text-sm font-medium rounded-md transition-colors"
					>
						Cancel
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}
