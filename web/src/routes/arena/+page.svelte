<script lang="ts">
	import Icon from '@iconify/svelte';
	import { onMount } from 'svelte';
	import { API_BASE } from '$lib/config';

	interface Standing {
		rank: number | null;
		team_id: string;
		team: string;
		attack: number;
		defense: number;
		sla: number;
		koth: number;
		total: number;
	}

	interface Hill {
		hill_id: string;
		name: string;
		controller?: string | null;
	}

	interface GameStatus {
		tick: number;
		round: number;
		tick_interval_seconds: number;
	}

	const POLL_MS = 5000;

	let standings: Standing[] = [];
	let hills: Hill[] = [];
	let status: GameStatus | null = null;

	let loading = true;
	let error = '';
	let gameActive = true;

	let initialized = false;
	let prevControllers: Record<string, string | null> = {};
	let flash = new Set<string>();

	let inFlight = false;
	let timer: ReturnType<typeof setInterval>;

	// Returns null when the endpoint reports the game is off (404 / {"error": ...}).
	async function fetchGame<T>(path: string): Promise<T | null> {
		const res = await fetch(`${API_BASE}/api/v1/arena${path}`, {
			headers: { 'Content-Type': 'application/json' }
		});
		if (res.status === 404) return null;
		if (!res.ok) throw new Error(`HTTP ${res.status}`);
		const body = await res.json().catch(() => null);
		if (body && body.error) return null;
		return body as T;
	}

	async function load() {
		if (inFlight) return;
		inFlight = true;
		try {
			const [sb, hl, st] = await Promise.all([
				fetchGame<{ standings: Standing[] }>('/scoreboard'),
				fetchGame<{ hills: Hill[] }>('/hills'),
				fetchGame<GameStatus>('/status')
			]);

			gameActive = !(sb === null && hl === null && st === null);
			if (!gameActive) {
				error = '';
				return;
			}

			standings = sb?.standings ?? [];
			applyHills(hl?.hills ?? []);
			status = st ?? status;
			initialized = true;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load game state';
		} finally {
			loading = false;
			inFlight = false;
		}
	}

	function applyHills(next: Hill[]) {
		const changed = new Set<string>();
		for (const h of next) {
			const controller = h.controller ?? null;
			if (initialized && prevControllers[h.hill_id] !== controller) {
				changed.add(h.hill_id);
			}
			prevControllers[h.hill_id] = controller;
		}
		hills = next;
		if (changed.size) {
			flash = new Set([...flash, ...changed]);
			for (const id of changed) {
				setTimeout(() => {
					flash.delete(id);
					flash = new Set(flash);
				}, 2400);
			}
		}
	}

	onMount(() => {
		load();
		timer = setInterval(load, POLL_MS);
		return () => clearInterval(timer);
	});

	// Deterministic hue from a team name/id so each team keeps a stable color.
	function teamHue(key: string): number {
		let h = 0;
		for (let i = 0; i < key.length; i++) {
			h = (Math.imul(h, 31) + key.charCodeAt(i)) >>> 0;
		}
		return h % 360;
	}

	function teamColor(key: string | null | undefined) {
		if (!key) {
			return {
				bg: 'hsl(30 6% 12%)',
				border: 'hsl(30 6% 26%)',
				text: 'hsl(30 6% 62%)',
				dot: 'hsl(30 6% 45%)'
			};
		}
		const hue = teamHue(key);
		return {
			bg: `hsl(${hue} 55% 16%)`,
			border: `hsl(${hue} 70% 48%)`,
			text: `hsl(${hue} 85% 82%)`,
			dot: `hsl(${hue} 70% 55%)`
		};
	}

	function fmt(n: number | null | undefined): string {
		return typeof n === 'number' && Number.isFinite(n) ? n.toFixed(1) : '—';
	}
</script>

<svelte:head>
	<title>Arena - Anvil</title>
</svelte:head>

<div class="max-w-[1600px] mx-auto px-4 sm:px-6 lg:px-8 py-6">
	{#if loading}
		<div class="flex items-center justify-center py-24">
			<Icon icon="mdi:loading" class="w-10 h-10 text-amber-500 animate-spin" />
		</div>
	{:else if error}
		<div class="bg-red-500/10 border border-red-500/20 rounded-lg p-6 text-center">
			<Icon icon="mdi:alert-circle" class="w-12 h-12 text-red-500 mx-auto mb-4" />
			<p class="text-red-400">{error}</p>
			<p class="text-stone-500 text-sm mt-2">Retrying every {POLL_MS / 1000}s…</p>
		</div>
	{:else if !gameActive}
		<div class="flex flex-col items-center justify-center py-24 text-center">
			<Icon icon="mdi:flag-off-outline" class="w-20 h-20 text-stone-700 mb-5" />
			<h1 class="text-2xl font-bold text-white mb-2">Game not active</h1>
			<p class="text-stone-500">The finals game is currently offline. This board updates automatically.</p>
		</div>
	{:else}
		<!-- Status bar -->
		<div class="flex flex-wrap items-center justify-between gap-4 mb-6">
			<div>
				<h1 class="text-2xl sm:text-3xl font-bold text-white">Finals Control Map</h1>
				<p class="text-stone-500 text-sm mt-0.5">King of the Hill · live</p>
			</div>
			<div class="flex items-center gap-3">
				<div class="flex items-center gap-2 bg-stone-900/70 border border-stone-800 rounded-lg px-4 py-2">
					<Icon icon="mdi:timer-sand" class="w-5 h-5 text-amber-500" />
					<span class="text-stone-400 text-sm">Tick</span>
					<span class="text-white font-bold text-lg tabular-nums">{status?.tick ?? '—'}</span>
				</div>
				<div class="flex items-center gap-2 bg-stone-900/70 border border-stone-800 rounded-lg px-4 py-2">
					<Icon icon="mdi:sync" class="w-5 h-5 text-amber-500" />
					<span class="text-stone-400 text-sm">Round</span>
					<span class="text-white font-bold text-lg tabular-nums">{status?.round ?? '—'}</span>
				</div>
				<div class="hidden sm:flex items-center gap-2 text-stone-600 text-xs">
					<span class="w-2 h-2 rounded-full bg-green-500 animate-pulse"></span>
					live · {POLL_MS / 1000}s
				</div>
			</div>
		</div>

		<!-- Control map -->
		<div class="mb-8" style="perspective: 1200px;">
			{#if hills.length === 0}
				<div class="bg-stone-900/50 rounded-xl border border-stone-800 p-10 text-center text-stone-500">
					No hills configured.
				</div>
			{:else}
				<div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-4">
					{#each hills as hill (hill.hill_id)}
						{@const c = teamColor(hill.controller)}
						{@const contested = !!hill.controller}
						<div
							class="rounded-xl border-2 p-5 min-h-[7.5rem] flex flex-col justify-between transition-shadow duration-300 {flash.has(
								hill.hill_id
							)
								? 'ring-4 ring-white/60 shadow-lg'
								: ''}"
							style="background: {c.bg}; border-color: {c.border};"
						>
							<div class="flex items-center justify-between gap-2">
								<span class="text-sm font-medium text-stone-300/90 truncate">{hill.name}</span>
								<Icon
									icon={contested ? 'mdi:flag' : 'mdi:flag-outline'}
									class="w-5 h-5 shrink-0"
									style="color: {c.dot};"
								/>
							</div>
							{#key hill.controller ?? '__none__'}
								<div class="flip-in" style="transform-origin: center;">
									{#if contested}
										<div class="text-xl sm:text-2xl font-extrabold leading-tight truncate" style="color: {c.text};">
											{hill.controller}
										</div>
										<div class="text-[0.7rem] uppercase tracking-wider text-stone-400/70 mt-0.5">holds</div>
									{:else}
										<div class="text-xl sm:text-2xl font-extrabold leading-tight text-stone-500">Uncontested</div>
										<div class="text-[0.7rem] uppercase tracking-wider text-stone-600 mt-0.5">open</div>
									{/if}
								</div>
							{/key}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<!-- Scoreboard -->
		<div class="bg-stone-900/50 rounded-xl border border-stone-800 overflow-hidden">
			<div class="px-5 py-4 border-b border-stone-800 flex items-center gap-2">
				<Icon icon="mdi:trophy" class="w-5 h-5 text-amber-500" />
				<h2 class="text-lg font-semibold text-white">Standings</h2>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full min-w-[720px]">
					<thead>
						<tr class="border-b border-stone-800 text-stone-400 text-sm">
							<th class="px-4 sm:px-6 py-3 text-left font-medium w-16">Rank</th>
							<th class="px-4 sm:px-6 py-3 text-left font-medium">Team</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Attack</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Defense</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">SLA</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">KotH</th>
							<th class="px-4 sm:px-6 py-3 text-right font-medium">Total</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-800">
						{#each standings as team, i (team.team_id)}
							{@const c = teamColor(team.team_id || team.team)}
							<tr class="hover:bg-stone-800/40 transition-colors {i === 0 ? 'bg-amber-500/5' : ''}">
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-white font-bold tabular-nums">
									{team.rank ?? '—'}
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap">
									<div class="flex items-center gap-2.5">
										<span class="w-3 h-3 rounded-full shrink-0" style="background: {c.dot};"></span>
										<span class="text-white font-medium truncate max-w-[220px]">{team.team}</span>
									</div>
								</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.attack)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right tabular-nums {team.defense < 0 ? 'text-red-400' : 'text-stone-300'}">{fmt(team.defense)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.sla)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-stone-300 tabular-nums">{fmt(team.koth)}</td>
								<td class="px-4 sm:px-6 py-3 whitespace-nowrap text-right text-amber-500 font-bold tabular-nums">{fmt(team.total)}</td>
							</tr>
						{/each}
						{#if standings.length === 0}
							<tr>
								<td colspan="7" class="px-6 py-10 text-center text-stone-500">No standings yet.</td>
							</tr>
						{/if}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>

<style>
	.flip-in {
		animation: flipIn 0.5s ease-out;
	}

	@keyframes flipIn {
		0% {
			transform: rotateX(90deg);
			opacity: 0;
		}
		60% {
			transform: rotateX(-12deg);
			opacity: 1;
		}
		100% {
			transform: rotateX(0);
			opacity: 1;
		}
	}
</style>
