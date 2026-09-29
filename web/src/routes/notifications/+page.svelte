<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon from '@iconify/svelte';
	import { api, type NotificationItem } from '$api';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { platformInfo } from '$lib/stores/platform';
	import { notificationSoundEnabled, playNotificationSound, setNotificationSound } from '$lib/notificationSound';

	let items: NotificationItem[] = [];
	let unreadCount = 0;
	let loading = true;
	let working = '';
	let error = '';
	let soundEnabled = false;
	let filter: 'all' | 'unread' | 'announcements' | 'team' = 'all';

	$: visible = items.filter((item) => {
		if (filter === 'unread') return !item.read;
		if (filter === 'announcements') return item.kind === 'announcement';
		if (filter === 'team') return item.audience === 'team' || item.audience === 'user';
		return true;
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const response = await api.getNotifications(100);
			items = response.items;
			unreadCount = response.unread_count;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Notifications are unavailable';
		} finally {
			loading = false;
		}
	}

	function toggleSound() {
		soundEnabled = !soundEnabled;
		setNotificationSound(soundEnabled);
		if (soundEnabled) playNotificationSound('preference-test');
	}

	onMount(() => {
		soundEnabled = notificationSoundEnabled();
		void load();
	});
	function syncHeader() {
		window.dispatchEvent(new Event('notifications:changed'));
	}

	async function markRead(item: NotificationItem, follow = false) {
		if (!item.read) {
			working = item.id;
			try {
				await api.markNotificationRead(item.id);
				items = items.map((candidate) => candidate.id === item.id ? { ...candidate, read: true } : candidate);
				unreadCount = Math.max(0, unreadCount - 1);
				syncHeader();
			} catch (e) {
				error = e instanceof Error ? e.message : 'Could not mark the notification read';
				working = '';
				return;
			}
			working = '';
		}
		if (follow && item.href) {
			if (item.href.startsWith('/')) await goto(item.href);
			else window.location.assign(item.href);
		}
	}

	async function markAllRead() {
		working = 'all';
		error = '';
		try {
			await api.markAllNotificationsRead();
			items = items.map((item) => ({ ...item, read: true }));
			unreadCount = 0;
			syncHeader();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not mark notifications read';
		} finally {
			working = '';
		}
	}

	function icon(item: NotificationItem) {
		if (item.kind === 'announcement') return 'mdi:bullhorn-outline';
		if (item.severity === 'critical') return 'mdi:alert-octagon-outline';
		if (item.severity === 'warning') return 'mdi:alert-outline';
		if (item.severity === 'success') return 'mdi:check-circle-outline';
		return 'mdi:information-outline';
	}

	function accent(item: NotificationItem) {
		return {
			critical: 'border-down/35 bg-down/[0.06]',
			warning: 'border-amber-500/30 bg-amber-500/[0.05]',
			success: 'border-emerald-500/25 bg-emerald-500/[0.04]',
			info: 'border-stone-800 bg-stone-900/25'
		}[item.severity];
	}

	function iconColor(item: NotificationItem) {
		return { critical: 'text-down', warning: 'text-amber-500', success: 'text-emerald-500', info: 'text-sky-400' }[item.severity];
	}

	function dateTime(value: string) {
		return new Date(value).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
	}
</script>

<svelte:head><title>Notifications · Anvil</title></svelte:head>

<div class="mx-auto max-w-5xl px-4 py-8 sm:px-6 lg:px-8">
	<PageHeader title="Notifications" subtitle="Organizer announcements and activity addressed to you or your team.">
		<div slot="actions">
			{#if $platformInfo?.notification_sound_allowed}
				<button type="button" on:click={toggleSound} aria-pressed={soundEnabled} class="mr-2 inline-flex items-center gap-2 rounded-md border border-stone-700 px-3.5 py-2 text-sm text-stone-300 transition-colors hover:bg-stone-900">
					<Icon icon={soundEnabled ? 'mdi:volume-high' : 'mdi:volume-off'} class="h-4 w-4" />
					{soundEnabled ? 'Sound on' : 'Sound off'}
				</button>
			{/if}
			{#if unreadCount > 0}
				<button on:click={markAllRead} disabled={working === 'all'} class="inline-flex items-center gap-2 rounded-md border border-stone-700 px-3.5 py-2 text-sm text-stone-300 transition-colors hover:bg-stone-900 disabled:opacity-50">
					<Icon icon={working === 'all' ? 'mdi:loading' : 'mdi:check-all'} class="h-4 w-4 {working === 'all' ? 'animate-spin' : ''}" />
					Mark all read
				</button>
			{/if}
		</div>
	</PageHeader>

	<div class="mb-5 flex flex-wrap gap-1" role="tablist" aria-label="Notification filters">
		{#each [
			{ id: 'all', label: 'All' },
			{ id: 'unread', label: `Unread (${unreadCount})` },
			{ id: 'announcements', label: 'Announcements' },
			{ id: 'team', label: 'Team activity' }
		] as option}
			<button
				role="tab"
				aria-selected={filter === option.id}
				on:click={() => (filter = option.id as typeof filter)}
				class="rounded-md border px-3 py-2 text-xs transition-colors {filter === option.id ? 'border-stone-600 bg-stone-800 text-stone-100' : 'border-stone-800 text-stone-500 hover:text-stone-300'}"
			>{option.label}</button>
		{/each}
	</div>

	{#if error}
		<div class="mb-4 flex items-center justify-between gap-3 rounded-lg border border-down/25 bg-down/[0.06] px-4 py-3 text-sm text-down" aria-live="polite">
			<span>{error}</span>
			<button on:click={load} class="shrink-0 underline underline-offset-2">Retry</button>
		</div>
	{/if}

	{#if loading}
		<Card hasHeader={false}>
			<div class="flex items-center justify-center gap-2 py-14 text-sm text-stone-500"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Loading notifications…</div>
		</Card>
	{:else if visible.length === 0}
		<Card hasHeader={false}>
			<EmptyState icon="mdi:bell-check-outline" text={filter === 'unread' ? 'You are all caught up.' : 'No notifications in this view.'} />
		</Card>
	{:else}
		<div class="space-y-3">
			{#each visible as item}
				<article class="relative rounded-lg border p-4 transition-colors {accent(item)} {item.read ? 'opacity-70' : ''}">
					{#if !item.read}<span class="absolute right-3 top-3 h-2 w-2 rounded-full bg-amber-500" title="Unread"></span>{/if}
					<div class="flex items-start gap-3 pr-4">
						<div class="mt-0.5 rounded-md border border-stone-800 bg-stone-950/60 p-2 {iconColor(item)}"><Icon icon={icon(item)} class="h-4 w-4" /></div>
						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<h2 class="text-sm font-medium text-stone-100">{item.title}</h2>
								{#if item.pinned}<span class="rounded-full bg-stone-800 px-2 py-0.5 text-[10px] text-stone-400">Pinned</span>{/if}
								<span class="text-[10px] uppercase tracking-wider text-stone-600">{item.kind === 'announcement' ? 'Organizer' : item.event_type.replaceAll('.', ' ')}</span>
							</div>
							<p class="mt-2 whitespace-pre-wrap text-sm leading-relaxed text-stone-400">{item.body}</p>
							<div class="mt-3 flex flex-wrap items-center gap-3 text-xs text-stone-600">
								<time datetime={item.publish_at}>{dateTime(item.publish_at)}</time>
								{#if item.href}<button on:click={() => markRead(item, true)} disabled={working === item.id} class="text-stone-400 underline underline-offset-2 hover:text-stone-200">Open</button>{/if}
								{#if !item.read}<button on:click={() => markRead(item)} disabled={working === item.id} class="text-stone-400 underline underline-offset-2 hover:text-stone-200">Mark read</button>{/if}
							</div>
						</div>
					</div>
				</article>
			{/each}
		</div>
	{/if}

	<p class="mt-6 text-center text-[11px] leading-relaxed text-stone-600">In-app delivery is authoritative. External channels, when configured, are best effort.</p>
</div>
