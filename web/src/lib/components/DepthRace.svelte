<script lang="ts">
	import { onMount } from 'svelte';
	import Card from '$lib/components/Card.svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import { api, type GradedRace } from '$api';
	import { instantTitle } from '$lib/time';

	export let slug: string;
	export let economy = false;

	// the grader reports on its own schedule; poll politely and on focus.
	const POLL_MS = 20_000;

	let race: GradedRace | null = null;
	let error = '';
	let loadedAt = 0;
	let now = Date.now();

	const pct = (v: number | null | undefined) => {
		const p = Math.max(0, Math.min(1, v ?? 0)) * 100;
		return p === 100 || p === 0 ? `${p}%` : `${p.toFixed(p < 10 ? 1 : 0)}%`;
	};

	// only flat, printable grader detail becomes chips; nested blobs stay out.
	$: metrics = Object.entries(race?.own?.raw ?? {})
		.filter(([, v]) => ['string', 'number', 'boolean'].includes(typeof v))
		.slice(0, 6) as [string, string | number | boolean][];

	$: ev = race?.evaluations;
	$: waitLeft = ev && ev.retry_after > 0 ? Math.max(0, Math.ceil(ev.retry_after - (now - loadedAt) / 1000)) : 0;
	$: fieldNote = race
		? `${race.teams} of ${race.attempted} team${race.attempted === 1 ? '' : 's'} on the board`
		: '';

	function clock(s: number) {
		const m = Math.floor(s / 60);
		return `${m}:${String(s % 60).padStart(2, '0')}`;
	}

	function ago(iso: string | null | undefined) {
		if (!iso) return '';
		const s = Math.max(0, Math.floor((now - Date.parse(iso)) / 1000));
		if (s < 60) return `${s}s ago`;
		if (s < 3600) return `${Math.floor(s / 60)}m ago`;
		return `${Math.floor(s / 3600)}h ago`;
	}

	let controller: AbortController | null = null;
	async function load() {
		controller?.abort();
		const c = new AbortController();
		controller = c;
		try {
			race = await api.getGradedRace(slug, { signal: c.signal });
			loadedAt = Date.now();
			error = '';
		} catch (e) {
			if (c.signal.aborted) return;
			error = e instanceof Error ? e.message : 'Failed to load the race';
		}
	}

	onMount(() => {
		let timer: ReturnType<typeof setTimeout>;
		const tick = setInterval(() => (now = Date.now()), 1000);
		const poll = async () => {
			clearTimeout(timer);
			if (document.visibilityState !== 'visible') return;
			await load();
			timer = setTimeout(poll, POLL_MS);
		};
		const onVisible = () => {
			if (document.visibilityState === 'visible') poll();
			else clearTimeout(timer);
		};
		poll();
		document.addEventListener('visibilitychange', onVisible);
		window.addEventListener('focus', onVisible);
		return () => {
			clearTimeout(timer);
			clearInterval(tick);
			controller?.abort();
			document.removeEventListener('visibilitychange', onVisible);
			window.removeEventListener('focus', onVisible);
		};
	});
</script>

<Card title="Depth race">
	<svelte:fragment slot="meta">
		{#if race?.last_update}
			<span class="inline-flex items-center gap-1.5 text-xs leading-none text-stone-500" title={instantTitle(race.last_update)}>
				<span class="h-1.5 w-1.5 rounded-full bg-amber-500/70 animate-pulse"></span>
				<span class="optical-label">{ago(race.last_update)}</span>
			</span>
		{/if}
	</svelte:fragment>

	{#if !race && !error}
		<div class="space-y-3">
			<div class="h-8 w-24 rounded bg-stone-800/60 animate-pulse"></div>
			<div class="h-1.5 rounded-full bg-stone-800/60 animate-pulse"></div>
		</div>
	{:else if !race}
		<p class="text-xs text-down">{error}</p>
	{:else}
		<div class="space-y-5">
			<!-- own best -->
			<div>
				<div class="flex items-end justify-between gap-3">
					<div>
						<p class="metadata-label text-stone-500 mb-1">Your best</p>
						<p class="text-2xl font-semibold tabular-nums leading-none {race.own && race.own.best > 0 ? 'text-amber-500' : 'text-stone-600'}">
							{pct(race.own?.best)}
						</p>
					</div>
					<div class="text-right text-xs leading-tight text-stone-500 tabular-nums">
						{#if race.own?.rank}
							<p><span class="text-stone-200 font-medium">#{race.own.rank}</span> of {race.teams}</p>
						{/if}
						{#if race.own?.points !== undefined}
							<p><span class="text-stone-300">{race.own.points}</span> / {race.base_points} pts</p>
						{/if}
					</div>
				</div>
				<div class="mt-3 h-1.5 rounded-full bg-stone-950 border border-stone-800 overflow-hidden">
					<div class="h-full rounded-full bg-amber-500/80 transition-all duration-700" style="width: {pct(race.own?.best)}"></div>
				</div>
				{#if metrics.length}
					<div class="mt-3 flex flex-wrap gap-1.5">
						{#each metrics as [k, v] (k)}
							<span class="inline-flex items-center gap-1 rounded border border-stone-800 bg-stone-950 px-2 py-1 text-[0.68rem] leading-none">
								<span class="text-stone-500">{k}</span>
								<span class="font-mono text-stone-200 tabular-nums">{v}</span>
							</span>
						{/each}
					</div>
				{/if}
				{#if !race.own}
					<p class="mt-3 text-xs text-stone-500 leading-relaxed">No score yet. Submit a solution through the challenge's own portal; its grader scores each run and your best counts.</p>
				{/if}
			</div>

			<!-- evaluations -->
			{#if ev}
				<div class="flex items-center justify-between gap-3 rounded-lg border border-stone-800 bg-stone-950 px-3 py-2 text-xs leading-none">
					<span class="inline-flex items-center gap-1.5 text-stone-400">
						<OpticalIcon icon="mdi:gauge" size={13} box={14} className="text-stone-500" />
						<span class="optical-label tabular-nums">
							{ev.used} evaluation{ev.used === 1 ? '' : 's'}
							{#if economy && ev.free_left > 0}· <span class="text-up">{ev.free_left} free left</span>
							{:else if economy && ev.next_cost > 0}· next <span class="text-amber-500">{ev.next_cost}</span> cr{/if}
						</span>
					</span>
					{#if ev.in_flight}
						<span class="inline-flex items-center gap-1.5 text-info">
							<span class="h-1.5 w-1.5 rounded-full bg-info animate-pulse"></span>
							<span class="optical-label">Evaluating</span>
						</span>
					{:else if waitLeft > 0}
						<span class="font-mono text-warn tabular-nums" title="Cooldown between evaluations">{clock(waitLeft)}</span>
					{:else}
						<span class="optical-label text-up">Ready</span>
					{/if}
				</div>
			{/if}

			<!-- the field -->
			<div>
				<div class="flex items-baseline justify-between mb-2">
					<p class="metadata-label text-stone-500">Field</p>
					<p class="text-[0.68rem] text-stone-600 tabular-nums">{fieldNote}</p>
				</div>
				{#if race.gated}
					<p class="text-xs text-stone-500 leading-relaxed">Launch this challenge to see how deep the field has gone.</p>
				{:else if race.hidden}
					<p class="text-xs text-stone-500 leading-relaxed">The scoreboard is frozen, so the field is hidden.</p>
				{:else if race.top.length === 0}
					<p class="text-xs text-stone-600">Nobody has scored yet.</p>
				{:else}
					<ol class="space-y-1.5">
						{#each race.top as e (e.rank)}
							<li class="grid grid-cols-[1.25rem_minmax(0,1fr)_3rem] items-center gap-2 text-xs leading-none">
								<span class="tabular-nums text-right {e.rank === 1 ? 'text-amber-500' : 'text-stone-600'}">{e.rank}</span>
								<div class="min-w-0">
									<div class="flex items-center justify-between gap-2 mb-1">
										<span class="optical-label truncate {e.own ? 'text-amber-500 font-medium' : 'text-stone-300'}">{e.team}</span>
									</div>
									<div class="h-1 rounded-full bg-stone-950 overflow-hidden">
										<div class="h-full rounded-full {e.own ? 'bg-amber-500/80' : 'bg-stone-500/70'}" style="width: {pct(e.best)}"></div>
									</div>
								</div>
								<span class="tabular-nums text-right {e.own ? 'text-amber-500' : 'text-stone-400'}" title={instantTitle(e.updated_at)}>{pct(e.best)}</span>
							</li>
						{/each}
					</ol>
				{/if}
			</div>
			{#if error}<p class="text-[0.68rem] text-warn">Live updates delayed: {error}</p>{/if}
		</div>
	{/if}
</Card>
