<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, ApiError } from '$api';
	import { auth } from '$stores/auth';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import { confirmDialog } from '$lib/stores/dialog';
	import { hasTeam } from '$lib/stores/platform';

	let loading = true;
	let teamsDisabled = false;
	let team: any = null;
	let error = '';

	let createName = '';
	let joinCode = '';
	let busy = false;
	let copied = false;
	let eco: any = null;
	let convertAmt = '';
	let conversionQuote: { points: number; credits: number; effective_rate: number; quote_version: number } | null = null;
	let quoteLoading = false;
	let quoteError = '';
	let quoteTimer: ReturnType<typeof setTimeout> | undefined;
	let quoteGeneration = 0;

	const inputClass =
		'w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors';
	const primaryBtn =
		'rounded-md bg-stone-100 text-stone-950 font-medium py-2.5 px-4 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors';

	async function load() {
		loading = true;
		error = '';
		teamsDisabled = false;
		try {
			const res = await api.getMyTeam();
			team = res.team;
			hasTeam.set(!!team); // clears the no-team banner / resumes the credits poll
			if (team) {
				try { eco = await api.getEconomy(); } catch { eco = null; }
			}
		} catch (e: any) {
			const msg = e?.message ?? 'Failed to load team';
			if (/team mode/i.test(msg)) teamsDisabled = true;
			else error = msg;
		} finally {
			loading = false;
		}
	}

	async function createTeam() {
		if (!createName.trim() || busy) return;
		busy = true;
		error = '';
		try {
			await api.createTeam(createName.trim());
			createName = '';
			await load();
		} catch (e: any) {
			error = e?.message ?? 'Failed to create team';
		} finally {
			busy = false;
		}
	}

	async function joinTeam() {
		if (!joinCode.trim() || busy) return;
		busy = true;
		error = '';
		try {
			await api.joinTeam(joinCode.trim());
			joinCode = '';
			await load();
		} catch (e: any) {
			error = e?.message ?? 'Failed to join team';
		} finally {
			busy = false;
		}
	}

	async function leaveTeam() {
		if (busy) return;
		if (!(await confirmDialog({ message: 'Leave this team? You\'ll need the join code to get back in, so keep it handy.', title: 'Leave team', confirmLabel: 'Leave', danger: true }))) return;
		busy = true;
		error = '';
		try {
			await api.leaveTeam();
			await load();
		} catch (e: any) {
			error = e?.message ?? 'Failed to leave team';
		} finally {
			busy = false;
		}
	}

	async function convert() {
		const pts = Number(convertAmt);
		if (!pts || pts <= 0 || busy || !conversionQuote || conversionQuote.points !== pts) return;
		const approvedQuote = conversionQuote;
		if (!(await confirmDialog({
			title: 'Convert team points',
			message: `Spend ${pts.toLocaleString()} points for ${approvedQuote.credits.toLocaleString(undefined, { maximumFractionDigits: 3 })} credits? This cannot be undone.`,
			confirmLabel: 'Convert',
			danger: true
		}))) return;
		busy = true;
		error = '';
		try {
			await api.convertPoints(pts, approvedQuote.quote_version);
			convertAmt = '';
			conversionQuote = null;
			quoteError = '';
			await load();
		} catch (e: any) {
			if (e instanceof ApiError && e.status === 409) {
				conversionQuote = null;
				await requestConversionQuote(pts);
				error = 'The conversion quote changed. Review the updated proceeds and confirm again.';
			} else {
				error = e?.message ?? 'convert failed';
			}
		} finally {
			busy = false;
		}
	}

	async function requestConversionQuote(points: number, generation = ++quoteGeneration) {
		quoteLoading = true;
		quoteError = '';
		try {
			const quote = await api.quotePointConversion(points);
			if (generation === quoteGeneration) conversionQuote = quote;
		} catch (e: any) {
			if (generation === quoteGeneration) quoteError = e?.message ?? 'Quote unavailable';
		} finally {
			if (generation === quoteGeneration) quoteLoading = false;
		}
	}

	function scheduleConversionQuote(raw: string) {
		if (quoteTimer) clearTimeout(quoteTimer);
		const generation = ++quoteGeneration;
		conversionQuote = null;
		quoteError = '';
		const points = Number(raw);
		if (!Number.isFinite(points) || points <= 0) {
			quoteLoading = false;
			return;
		}
		quoteLoading = true;
		quoteTimer = setTimeout(() => {
			quoteTimer = undefined;
			void requestConversionQuote(points, generation);
		}, 250);
	}

	async function bailout() {
		if (busy) return;
		busy = true;
		error = '';
		try {
			await api.economyBailout();
			await load();
		} catch (e: any) {
			error = e?.message ?? 'bailout failed';
		} finally {
			busy = false;
		}
	}

	async function copyCode() {
		if (!team?.join_code) return;
		try {
			await navigator.clipboard.writeText(team.join_code);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard unavailable */
		}
	}

	// challenges currently holding one of the team's 3 open slots (solved ones are excluded)
	$: openNow = (eco?.open ?? []).filter((o: any) => o.status === 'open');

	onMount(load);
	onDestroy(() => {
		if (quoteTimer) clearTimeout(quoteTimer);
	});
</script>

<svelte:head><title>Team · Anvil</title></svelte:head>

<div class="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8 py-8">
	<PageHeader title="Team" subtitle="Your team's roster, join code, and standing." />

	{#if !$auth.isAuthenticated}
		<Card hasHeader={false}>
			<EmptyState icon="mdi:account-group-outline" text="Sign in to create or join a team.">
				<a href="/login" class="mt-4 {primaryBtn} inline-block">Login</a>
			</EmptyState>
		</Card>
	{:else if loading}
		<Card hasHeader={false}>
			<div class="flex items-center justify-center gap-2 py-10 text-stone-500 text-sm">
				<Icon icon="mdi:loading" class="w-4 h-4 animate-spin" /> Loading…
			</div>
		</Card>
	{:else if teamsDisabled}
		<Card hasHeader={false}>
			<EmptyState icon="mdi:account-off-outline" text="Team mode is not enabled for this event." />
		</Card>
	{:else}
		{#if error}
			<div class="mb-4 rounded-md border border-down/20 bg-down/10 px-3 py-2 text-xs text-down" aria-live="polite">
				{error}
			</div>
		{/if}

		{#if team}
			<Card title={team.name}>
				<svelte:fragment slot="meta">
					<span class="text-xs text-stone-500">{(team.members ?? []).length} member{(team.members ?? []).length === 1 ? '' : 's'}</span>
				</svelte:fragment>

				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<p class="metadata-label text-stone-500 mb-1">Team Score</p>
						<p class="text-2xl font-semibold text-stone-100 tabular-nums">{team.total_score ?? 0}</p>
					</div>
					<div>
						<p class="metadata-label text-stone-500 mb-1">Join Code</p>
						<div class="flex items-center gap-2">
							<code class="rounded bg-stone-950 border border-stone-800 px-2 py-1 text-sm text-amber-500">{team.join_code}</code>
							<button on:click={copyCode} class="text-stone-500 hover:text-stone-200 transition-colors" title="Copy join code" aria-label="Copy join code">
								<Icon icon={copied ? 'mdi:check' : 'mdi:content-copy'} class="w-4 h-4" />
							</button>
						</div>
						<p class="text-xs text-stone-600 mt-1">Share this with teammates to let them join.</p>
					</div>
				</div>

				<div class="mt-5">
					<p class="metadata-label text-stone-500 mb-2">Members</p>
					<div class="divide-y divide-stone-800/70 border border-stone-800 rounded-md overflow-hidden">
						{#each (team.members ?? []) as m}
							<div class="flex items-center justify-between px-3 py-2.5">
								<span class="text-sm text-stone-200">{m.display_name || m.username}</span>
								<span class="text-xs text-stone-500 tabular-nums">{m.solve_count} solve{m.solve_count === 1 ? '' : 's'}</span>
							</div>
						{/each}
					</div>
				</div>

				<div class="mt-5 flex justify-end">
					<button
						on:click={leaveTeam}
						disabled={busy}
						class="rounded-md border border-down/30 bg-down/10 text-down py-2 px-4 text-sm hover:bg-down/20 disabled:opacity-40 transition-colors"
					>
						Leave Team
					</button>
				</div>
			</Card>

			{#if eco}
				<Card title="Economy" className="mt-4">
					<div class="grid gap-4 sm:grid-cols-2">
						<div>
							<p class="metadata-label text-stone-500 mb-1">Credits</p>
							<p class="text-2xl font-semibold text-amber-500 tabular-nums">{Math.floor(eco.credits)}</p>
						</div>
						<div>
							<p class="metadata-label text-stone-500 mb-1">Points</p>
							<p class="text-2xl font-semibold text-stone-100 tabular-nums">{Math.round(eco.points)}</p>
						</div>
					</div>

					<form on:submit|preventDefault={convert} class="mt-4 flex gap-2">
						<input class={inputClass} type="number" min="1" bind:value={convertAmt} on:input={(event) => scheduleConversionQuote((event.currentTarget as HTMLInputElement).value)} placeholder="Convert points → credits" aria-label="Points to convert to credits" />
						<button type="submit" disabled={busy || !conversionQuote || conversionQuote.points !== Number(convertAmt)} class="{primaryBtn} whitespace-nowrap">Convert</button>
					</form>
					{#if quoteLoading}
						<p class="mt-1 text-xs text-stone-500">Calculating current quote…</p>
					{:else if conversionQuote}
						<p class="mt-1 text-xs text-stone-500">
							{conversionQuote.points.toLocaleString()} points →
							<span class="font-medium tabular-nums text-amber-500">{conversionQuote.credits.toLocaleString(undefined, { maximumFractionDigits: 3 })} credits</span>
							at {conversionQuote.effective_rate.toLocaleString(undefined, { maximumFractionDigits: 4 })} credits/point.
						</p>
					{:else if quoteError}
						<p class="mt-1 text-xs text-down">{quoteError}</p>
					{:else}
						<p class="text-xs text-stone-600 mt-1">Enter an amount to see the current quote. The rate falls with each block.</p>
					{/if}

					{#if eco.credits < 50 && !eco.bailout_used && (eco.open ?? []).filter((o: any) => o.status === 'open').length === 0}
						<button on:click={bailout} disabled={busy} class="mt-3 w-full rounded-md border border-amber-500/30 bg-amber-500/10 text-amber-500 py-2 text-sm hover:bg-amber-500/20 disabled:opacity-40 transition-colors">
							Request bailout
						</button>
						<p class="text-xs text-stone-600 mt-1">A one-time top-up when you can no longer afford a launch and cannot convert points to cover it.</p>
					{/if}
				</Card>

				{#if openNow.length}
					<Card title="Open now ({openNow.length}/3)" className="mt-4">
						<p class="text-xs text-stone-500 mb-3">These count toward your 3 open limit (static challenges and any teammate's opens count too). Solve or abandon one to open another.</p>
						<ul class="space-y-2">
							{#each openNow as o}
								<li class="flex items-center justify-between rounded-md border border-stone-800 bg-stone-950 px-3 py-2">
									<a href="/challenges/{o.slug}" class="text-sm text-stone-200 hover:text-stone-50 truncate min-w-0">{o.name}</a>
									<span class="text-xs text-stone-500 tabular-nums ml-2 shrink-0">open</span>
								</li>
							{/each}
						</ul>
					</Card>
				{/if}
			{/if}
		{:else}
			<div class="grid gap-4 sm:grid-cols-2">
				<Card title="Create a Team">
					<p class="text-sm text-stone-500 mb-3">Start a team and share the join code with your teammates.</p>
					<form on:submit|preventDefault={createTeam} class="space-y-3">
						<input class={inputClass} bind:value={createName} maxlength="100" placeholder="Team name" aria-label="Team name" />
						<button type="submit" disabled={busy || !createName.trim()} class="w-full {primaryBtn}">Create Team</button>
					</form>
				</Card>

				<Card title="Join a Team">
					<p class="text-sm text-stone-500 mb-3">Have a join code from a teammate? Enter it here.</p>
					<form on:submit|preventDefault={joinTeam} class="space-y-3">
						<input class={inputClass} bind:value={joinCode} placeholder="Join code" aria-label="Team join code" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
						<button type="submit" disabled={busy || !joinCode.trim()} class="w-full {primaryBtn}">Join Team</button>
					</form>
				</Card>
			</div>
		{/if}
	{/if}
</div>
