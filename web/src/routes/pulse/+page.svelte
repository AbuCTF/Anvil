<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, ApiError, type MarketPulseResponse } from '$api';
	import { auth } from '$stores/auth';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

	let pulse: MarketPulseResponse | null = null;
	let loading = true;
	let refreshing = false;
	let error = '';
	let search = '';
	let heat = 'all';
	let refreshTimer: ReturnType<typeof setInterval> | undefined;

	const heatOptions = ['all', 'hot', 'active', 'warming', 'quiet'];

	$: filteredField = (pulse?.field ?? []).filter((challenge) => {
		const needle = search.trim().toLowerCase();
		const matchesSearch = !needle || `${challenge.name} ${challenge.category} ${challenge.difficulty}`.toLowerCase().includes(needle);
		return matchesSearch && (heat === 'all' || challenge.heat === heat);
	});

	async function load(background = false) {
		if (background) refreshing = true;
		else loading = true;
		error = '';
		try {
			pulse = await api.getMarketPulse();
		} catch (e) {
			if (e instanceof ApiError && e.status === 403) {
				error = 'Join a team to use Market Pulse.';
			} else {
				error = e instanceof Error ? e.message : 'Market Pulse is unavailable';
			}
		} finally {
			loading = false;
			refreshing = false;
		}
	}

	onMount(() => {
		void load();
		refreshTimer = setInterval(() => {
			if (!document.hidden) void load(true);
		}, 60_000);
		return () => {
			if (refreshTimer) clearInterval(refreshTimer);
		};
	});

	function number(value: number, digits = 0) {
		return value.toLocaleString(undefined, { maximumFractionDigits: digits });
	}

	function dateTime(value: string) {
		return new Date(value).toLocaleString(undefined, {
			month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
		});
	}

	function remaining(value?: string) {
		if (!value) return 'No timer';
		const seconds = Math.max(0, Math.floor((new Date(value).getTime() - Date.now()) / 1000));
		const hours = Math.floor(seconds / 3600);
		const minutes = Math.floor((seconds % 3600) / 60);
		if (hours > 0) return `${hours}h ${minutes}m`;
		return `${minutes}m`;
	}

	function title(value: string) {
		return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
	}

	function heatClass(value: string) {
		return {
			hot: 'border-down/30 bg-down/10 text-down',
			active: 'border-amber-500/30 bg-amber-500/10 text-amber-500',
			warming: 'border-sky-500/30 bg-sky-500/10 text-sky-400',
			quiet: 'border-stone-800 bg-stone-900/50 text-stone-500'
		}[value] ?? 'border-stone-800 bg-stone-900/50 text-stone-500';
	}

	function noticeClass(value: string) {
		return value === 'warning'
			? 'border-amber-500/25 bg-amber-500/[0.07] text-amber-100'
			: 'border-sky-500/20 bg-sky-500/[0.06] text-sky-100';
	}

	function directionIcon(value: string) {
		return value === 'accelerating' ? 'mdi:trending-up'
			: value === 'cooling' ? 'mdi:trending-down'
			: value === 'steady' ? 'mdi:trending-neutral'
			: 'mdi:minus';
	}
</script>

<svelte:head><title>Market Pulse · Anvil</title></svelte:head>

<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
	<div class="flex items-start justify-between gap-4">
		<PageHeader title="Market Pulse" subtitle="Exact team runway. Delayed, privacy-safe field signals." />
		{#if pulse}
			<button
				on:click={() => load(true)}
				disabled={refreshing}
				class="mt-1 inline-flex h-9 items-center gap-2 rounded-md border border-stone-800 px-3 text-xs text-stone-400 transition-colors hover:border-stone-700 hover:text-stone-200 disabled:opacity-50"
			>
				<Icon icon="mdi:refresh" class="h-4 w-4 {refreshing ? 'animate-spin' : ''}" />
				Refresh
			</button>
		{/if}
	</div>

	{#if !$auth.isAuthenticated}
		<Card hasHeader={false}>
			<EmptyState icon="mdi:pulse" text="Sign in and join a team to view Market Pulse.">
				<a href="/login" class="mt-4 inline-block rounded-md bg-stone-100 px-4 py-2.5 text-sm font-medium text-stone-950 hover:bg-stone-50">Login</a>
			</EmptyState>
		</Card>
	{:else if loading}
		<Card hasHeader={false}>
			<div class="flex items-center justify-center gap-2 py-14 text-sm text-stone-500">
				<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Building the latest pulse…
			</div>
		</Card>
	{:else if error || !pulse}
		<Card hasHeader={false}>
			<EmptyState icon="mdi:pulse" text={error || 'Market Pulse is unavailable.'}>
				<a href="/team" class="mt-4 inline-block rounded-md border border-stone-800 px-4 py-2.5 text-sm text-stone-300 hover:bg-stone-900">Open Team</a>
			</EmptyState>
		</Card>
	{:else}
		<div class="mb-5 flex flex-col gap-2 rounded-lg border border-stone-800 bg-stone-900/35 px-4 py-3 text-xs text-stone-400 sm:flex-row sm:items-center sm:justify-between">
			<div class="flex items-center gap-2">
				<Icon icon="mdi:shield-check-outline" class="h-4 w-4 shrink-0 text-emerald-500" />
				<span>Field data is delayed {Math.round(pulse.policy.delay_seconds / 60)} minutes, published every {Math.round(pulse.policy.cadence_seconds / 60)} minutes, and hidden below {pulse.policy.min_anonymity} teams.</span>
			</div>
			<span class="shrink-0 tabular-nums">As of {dateTime(pulse.policy.source_cutoff)}</span>
		</div>

		{#if pulse.notices.length}
			<div class="mb-5 space-y-2" aria-live="polite">
				{#each pulse.notices as notice}
					<div class="flex items-start gap-3 rounded-lg border px-4 py-3 text-sm {noticeClass(notice.severity)}">
						<Icon icon={notice.severity === 'warning' ? 'mdi:alert-outline' : 'mdi:information-outline'} class="mt-0.5 h-4 w-4 shrink-0" />
						<p class="min-w-0 flex-1 leading-relaxed">{notice.message}</p>
						{#if notice.href}<a href={notice.href} class="shrink-0 text-xs underline underline-offset-2">Review</a>{/if}
					</div>
				{/each}
			</div>
		{/if}

		<section aria-labelledby="runway-heading">
			<div class="mb-3 flex items-end justify-between gap-3">
				<div>
					<p class="metadata-label text-stone-500">Private to your team</p>
					<h2 id="runway-heading" class="mt-1 text-lg font-semibold text-stone-100">Your runway</h2>
				</div>
				<a href="/team" class="text-xs text-stone-500 underline-offset-2 hover:text-stone-300 hover:underline">Economy actions</a>
			</div>

			<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
				<div class="rounded-lg border border-amber-500/20 bg-amber-500/[0.06] p-4">
					<p class="metadata-label text-amber-500/70">Credits</p>
					<p class="mt-2 text-2xl font-semibold text-amber-500 tabular-nums">{number(pulse.team.credits, 3)}</p>
					<p class="mt-1 text-xs text-stone-500">Spendable team budget</p>
				</div>
				<div class="rounded-lg border border-stone-800 bg-stone-900/35 p-4">
					<p class="metadata-label text-stone-500">Banked points</p>
					<p class="mt-2 text-2xl font-semibold text-stone-100 tabular-nums">{number(pulse.team.points, 3)}</p>
					<p class="mt-1 text-xs text-stone-500">Next conversion rate {number(pulse.team.next_p2c_rate, 4)}×</p>
				</div>
				<div class="rounded-lg border border-stone-800 bg-stone-900/35 p-4">
					<p class="metadata-label text-stone-500">Open slots</p>
					<p class="mt-2 text-2xl font-semibold text-stone-100 tabular-nums">{pulse.team.open_slots_used}<span class="text-base text-stone-600">/{pulse.team.open_slots_total}</span></p>
					<p class="mt-1 text-xs text-stone-500">Shared by every teammate</p>
				</div>
				<div class="rounded-lg border border-stone-800 bg-stone-900/35 p-4">
					<p class="metadata-label text-stone-500">Settlement value</p>
					<p class="mt-2 text-2xl font-semibold text-stone-100 tabular-nums">{number(pulse.team.settlement_exposure, 3)}</p>
					<p class="mt-1 text-xs text-stone-500">Points if current credits settle now</p>
				</div>
			</div>

			<div class="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
				{#each pulse.team.affordability as item}
					<div class="rounded-lg border p-3 {item.affordable ? 'border-stone-800 bg-stone-900/25' : 'border-stone-900 bg-stone-950/30 opacity-65'}">
						<div class="flex items-center justify-between gap-2">
							<span class="text-sm font-medium text-stone-200">{title(item.difficulty)}</span>
							<span class="rounded-full px-2 py-0.5 text-[10px] font-medium {item.affordable ? 'bg-emerald-500/10 text-emerald-400' : 'bg-stone-900 text-stone-600'}">{item.affordable ? 'Affordable' : 'Out of reach'}</span>
						</div>
						<p class="mt-2 text-xs text-stone-500"><span class="tabular-nums text-stone-300">{number(item.cost)}</span> credits per launch · runway {item.count}</p>
					</div>
				{/each}
			</div>

			{#if pulse.team.open.length}
				<div class="mt-3 grid gap-3 lg:grid-cols-2">
					{#each pulse.team.open as item}
						<a href="/challenges/{item.slug}" class="group rounded-lg border border-stone-800 bg-stone-900/25 p-4 transition-colors hover:border-stone-700">
							<div class="flex items-start justify-between gap-3">
								<div class="min-w-0">
									<p class="truncate text-sm font-medium text-stone-200 group-hover:text-stone-50">{item.name}</p>
									<p class="mt-1 text-xs text-stone-500">{title(item.difficulty)} · value {number(item.current_value, 2)} · {item.wrong_submissions} wrong</p>
								</div>
								<span class="shrink-0 text-sm font-medium text-amber-500 tabular-nums">{remaining(item.expires_at)}</span>
							</div>
							{#if item.next_extension_cost !== undefined}
								<p class="mt-3 border-t border-stone-800/70 pt-2 text-xs text-stone-600">Next extension: {number(item.next_extension_cost, 3)} credits</p>
							{/if}
						</a>
					{/each}
				</div>
			{/if}
		</section>

		<section class="mt-8" aria-labelledby="field-heading">
			<div class="mb-3 flex flex-col gap-3 md:flex-row md:items-end md:justify-between">
				<div>
					<p class="metadata-label text-stone-500">Delayed and coarsened</p>
					<h2 id="field-heading" class="mt-1 text-lg font-semibold text-stone-100">Field pulse</h2>
				</div>
				{#if !pulse.policy.field_hidden}
					<div class="flex flex-col gap-2 sm:flex-row">
						<div class="relative">
							<Icon icon="mdi:magnify" class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-stone-600" />
							<input bind:value={search} placeholder="Find a challenge" class="w-full rounded-md border border-stone-800 bg-stone-900/50 py-2 pl-9 pr-3 text-xs text-stone-200 outline-none placeholder:text-stone-600 focus:border-stone-700 sm:w-56" />
						</div>
						<div class="flex gap-1 overflow-x-auto pb-1 sm:pb-0">
							{#each heatOptions as option}
								<button on:click={() => (heat = option)} class="shrink-0 rounded-md border px-2.5 py-2 text-xs transition-colors {heat === option ? 'border-stone-600 bg-stone-800 text-stone-100' : 'border-stone-800 text-stone-500 hover:text-stone-300'}">{title(option)}</button>
							{/each}
						</div>
					</div>
				{/if}
			</div>

			{#if pulse.policy.field_hidden}
				<div class="rounded-lg border border-stone-800 bg-stone-900/30 px-5 py-12 text-center">
					<Icon icon="mdi:eye-off-outline" class="mx-auto h-7 w-7 text-stone-600" />
					<p class="mt-3 text-sm font-medium text-stone-300">Field signals are blind</p>
					<p class="mx-auto mt-1 max-w-lg text-xs leading-relaxed text-stone-500">{pulse.policy.field_hidden_reason}</p>
				</div>
			{:else if filteredField.length === 0}
				<div class="rounded-lg border border-stone-800 bg-stone-900/20 px-5 py-10 text-center text-sm text-stone-500">No challenges match these filters.</div>
			{:else}
				<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
					{#each filteredField as challenge}
						<a href="/challenges/{challenge.slug}" class="group rounded-lg border border-stone-800 bg-stone-900/25 p-4 transition-colors hover:border-stone-700 hover:bg-stone-900/40">
							<div class="flex items-start justify-between gap-3">
								<div class="min-w-0">
									<p class="truncate text-sm font-medium text-stone-200 group-hover:text-stone-50">{challenge.name}</p>
									<p class="mt-1 truncate text-xs text-stone-600">{challenge.category} · {title(challenge.difficulty)}</p>
								</div>
								<span class="shrink-0 rounded-full border px-2 py-1 text-[10px] font-medium {heatClass(challenge.heat)}">{title(challenge.heat)}</span>
							</div>
							<div class="mt-4 grid grid-cols-2 gap-2 border-t border-stone-800/70 pt-3 text-xs">
								<div>
									<p class="text-stone-600">Solve band</p>
									<p class="mt-1 text-stone-300">{title(challenge.solve_band)}</p>
								</div>
								<div>
									<p class="text-stone-600">Direction</p>
									<p class="mt-1 inline-flex items-center gap-1 text-stone-300"><Icon icon={directionIcon(challenge.direction)} class="h-3.5 w-3.5" /> {title(challenge.direction)}</p>
								</div>
							</div>
							{#if challenge.signal_quality === 'limited_for_graded'}
								<p class="mt-3 text-[10px] leading-relaxed text-stone-600">Graded progress stays private; this card only reflects qualifying capture events.</p>
							{/if}
						</a>
					{/each}
				</div>
			{/if}
		</section>

		<p class="mt-6 text-center text-[11px] leading-relaxed text-stone-600">
			Market Pulse never shows team names, exact opponent balances, private opens, or sub-threshold movement. Your own runway is exact; field signals are intentionally coarse.
		</p>
	{/if}
</div>
