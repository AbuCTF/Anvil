<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import { platformInfo } from '$lib/stores/platform';
	import { authToken } from '$lib/stores/auth';

	interface FinalsServiceBreakdown {
		slug: string;
		name: string;
		steal: number;
		hold: number;
		up: number;
	}
	interface FinalsRow {
		rank: number;
		team_id: string;
		team_name: string;
		steal: number;
		hold: number;
		up: number;
		ad: number;
		koth: number;
		total: number;
		services: FinalsServiceBreakdown[];
	}

	// reveal state
	let rows: FinalsRow[] = [];
	let frozen = false;
	let loading = true;
	let error = '';

	// sorted 15→1 (index 0 = last place to reveal first)
	$: queue = [...rows].sort((a, b) => b.rank - a.rank);

	// how many have been revealed (from the bottom up)
	let revealed = 0;
	// auto-play
	let autoPlay = false;
	let autoTimer: ReturnType<typeof setTimeout> | null = null;
	let autoDelay = 3000; // ms per reveal

	// animation state: track entry per rank slot
	let animating = false;

	$: isFinalsMode = !!$platformInfo?.game_state;
	$: isFrozen = !!$platformInfo?.game_frozen;

	// currently shown rows in display order (top=1st), only revealed ones
	$: displayed = queue.slice(0, revealed).reverse();

	// next row to reveal
	$: nextRow = revealed < queue.length ? queue[revealed] : null;

	// done when all revealed
	$: allDone = revealed >= queue.length;

	async function loadScoreboard() {
		loading = true;
		error = '';
		try {
			const headers: Record<string, string> = {};
			if ($authToken) headers['Authorization'] = `Bearer ${$authToken}`;
			const res = await fetch('/api/v1/arena/scoreboard', { headers });
			if (!res.ok) throw new Error(`${res.status}`);
			const data = await res.json();
			rows = (data.rows ?? []) as FinalsRow[];
			frozen = !!data.frozen;
		} catch (e: unknown) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function reveal() {
		if (revealed >= queue.length || animating) return;
		animating = true;
		revealed += 1;
		await tick();
		// brief animation window
		setTimeout(() => { animating = false; }, 600);
	}

	function startAuto() {
		autoPlay = true;
		scheduleNext();
	}

	function stopAuto() {
		autoPlay = false;
		if (autoTimer) { clearTimeout(autoTimer); autoTimer = null; }
	}

	function scheduleNext() {
		if (!autoPlay || allDone) { autoPlay = false; return; }
		autoTimer = setTimeout(async () => {
			await reveal();
			scheduleNext();
		}, autoDelay);
	}

	function fmt1(n: number) { return n.toFixed(1); }

	onMount(() => { loadScoreboard(); });
	onDestroy(() => { if (autoTimer) clearTimeout(autoTimer); });
</script>

<svelte:head><title>Final Reveal - H7CTF 2026</title></svelte:head>

{#if !isFinalsMode}
	<div class="flex min-h-[60vh] items-center justify-center">
		<p class="text-stone-500">finals mode not active</p>
	</div>
{:else if loading}
	<div class="flex min-h-[60vh] items-center justify-center">
		<p class="text-stone-500 animate-pulse">loading scoreboard&hellip;</p>
	</div>
{:else if error}
	<div class="flex min-h-[60vh] items-center justify-center">
		<p class="text-red-400">error: {error}</p>
	</div>
{:else if !frozen}
	<div class="flex min-h-[60vh] flex-col items-center justify-center gap-4">
		<p class="text-2xl font-bold text-stone-400">scoreboard not frozen</p>
		<p class="text-sm text-stone-600">the reveal page is only active during the freeze window</p>
		<button
			class="rounded border border-stone-700 px-4 py-1.5 text-sm text-stone-400 hover:border-stone-500 hover:text-stone-200 transition-colors"
			on:click={loadScoreboard}
		>check again</button>
	</div>
{:else}
	<!-- full-screen reveal stage -->
	<div class="min-h-screen bg-stone-950 px-4 pb-16 pt-8 font-mono">

		<!-- header -->
		<div class="mb-8 text-center">
			<p class="text-xs uppercase tracking-[0.3em] text-stone-600">H7CTF 2026 Finals</p>
			<h1 class="mt-1 text-3xl font-bold text-stone-100">Final Standings</h1>
			{#if allDone}
				<p class="mt-2 text-sm text-[#9bcea8]">all {rows.length} teams revealed</p>
			{:else}
				<p class="mt-2 text-sm text-stone-600">
					{revealed} / {queue.length} revealed
					&mdash; next: <span class="text-amber-400">#{nextRow?.rank} {nextRow?.team_name}</span>
				</p>
			{/if}
		</div>

		<!-- control bar -->
		<div class="mx-auto mb-8 flex max-w-2xl items-center justify-center gap-3">
			{#if !allDone}
				<button
					class="rounded border border-stone-700 px-5 py-2 text-sm text-stone-300 transition-colors hover:border-[#9bcea8] hover:text-[#9bcea8] disabled:opacity-40"
					disabled={animating}
					on:click={reveal}
				>reveal next</button>
				{#if autoPlay}
					<button
						class="rounded border border-amber-700 px-4 py-2 text-sm text-amber-400 transition-colors hover:border-amber-400"
						on:click={stopAuto}
					>pause auto</button>
				{:else}
					<button
						class="rounded border border-stone-700 px-4 py-2 text-sm text-stone-400 transition-colors hover:border-stone-500"
						on:click={startAuto}
					>auto-play</button>
				{/if}
				<label class="flex items-center gap-1.5 text-xs text-stone-600">
					delay
					<select
						class="rounded bg-stone-900 border border-stone-800 px-1 py-0.5 text-stone-400"
						bind:value={autoDelay}
					>
						<option value={1500}>1.5s</option>
						<option value={3000}>3s</option>
						<option value={5000}>5s</option>
						<option value={8000}>8s</option>
					</select>
				</label>
			{/if}
			<button
				class="rounded border border-stone-800 px-3 py-2 text-xs text-stone-600 transition-colors hover:border-stone-700 hover:text-stone-400"
				on:click={loadScoreboard}
			>reload</button>
		</div>

		<!-- standings board -->
		<div class="mx-auto max-w-3xl space-y-2">
			{#each displayed as row (row.team_id)}
				{@const isFirst = row.rank === 1}
				<div
					class="reveal-row flex items-center gap-4 rounded-lg border px-5 py-4 transition-all duration-500
					{isFirst
						? 'border-amber-500/60 bg-amber-950/30 ring-1 ring-amber-500/30'
						: row.rank <= 3
						? 'border-stone-600 bg-stone-900/60'
						: 'border-stone-800 bg-stone-900/30'}"
				>
					<!-- rank badge -->
					<div class="w-10 shrink-0 text-center">
						{#if isFirst}
							<span class="text-2xl">&#x1F451;</span>
						{:else if row.rank === 2}
							<span class="text-xl font-bold text-stone-300">#2</span>
						{:else if row.rank === 3}
							<span class="text-xl font-bold text-stone-400">#3</span>
						{:else}
							<span class="text-lg font-bold text-stone-600">#{row.rank}</span>
						{/if}
					</div>

					<!-- team name -->
					<div class="min-w-0 flex-1">
						<p class="truncate font-semibold
						{isFirst ? 'text-xl text-amber-300' : row.rank <= 3 ? 'text-base text-stone-200' : 'text-sm text-stone-300'}">
							{row.team_name}
						</p>
						<!-- breakdown -->
						<div class="mt-0.5 flex flex-wrap gap-x-3 gap-y-0 text-[0.65rem] text-stone-600">
							<span>AD <span class="text-stone-400">{fmt1(row.ad)}</span></span>
							<span>KotH <span class="text-stone-400">{fmt1(row.koth)}</span></span>
							<span>STEAL <span class="text-stone-400">{fmt1(row.steal)}</span></span>
							<span>HOLD <span class="text-stone-400">{fmt1(row.hold)}</span></span>
						</div>
					</div>

					<!-- total score -->
					<div class="shrink-0 text-right">
						<p class="tabular-nums font-bold
						{isFirst ? 'text-2xl text-amber-300' : row.rank <= 3 ? 'text-xl text-stone-200' : 'text-base text-stone-300'}">
							{fmt1(row.total)}
						</p>
						<p class="text-[0.6rem] text-stone-600 uppercase tracking-wider">total</p>
					</div>
				</div>
			{/each}

			<!-- pending slots placeholder -->
			{#each Array(Math.max(0, queue.length - revealed)).fill(0) as _, i}
				<div class="flex items-center gap-4 rounded-lg border border-stone-900 bg-stone-950/50 px-5 py-4">
					<div class="w-10 shrink-0 text-center">
						<span class="text-stone-800">#{queue.length - revealed - i}</span>
					</div>
					<div class="flex-1">
						<div class="h-3 w-32 animate-pulse rounded bg-stone-900"></div>
					</div>
					<div class="h-5 w-16 animate-pulse rounded bg-stone-900"></div>
				</div>
			{/each}
		</div>

	</div>
{/if}

<style>
	.reveal-row {
		animation: slideIn 0.5s cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes slideIn {
		from {
			opacity: 0;
			transform: translateX(40px) scale(0.97);
		}
		to {
			opacity: 1;
			transform: translateX(0) scale(1);
		}
	}
</style>
