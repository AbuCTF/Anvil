<script lang="ts">
	import Icon from '@iconify/svelte';
	import { eventClock } from '$lib/stores/platform';
	import { formatLocalDateTimeWithZone, formatLocalTimeWithZone } from '$lib/time';

	// the clock hit zero but the board hasn't landed yet (jittered refetch in flight)
	$: unlocking = $eventClock.phase === 'live' || $eventClock.phase === 'ended';
	$: startMs = $eventClock.startMs;
	$: startText = startMs
		? new Date(startMs).toDateString() === new Date().toDateString()
			? formatLocalTimeWithZone(startMs)
			: formatLocalDateTimeWithZone(startMs)
		: '';
	$: units = countdownUnits($eventClock.untilStart);

	function countdownUnits(ms: number): { value: string; unit: string }[] {
		const t = Math.max(0, Math.floor(ms / 1000));
		const parts = [
			{ value: String(Math.floor(t / 86_400)), unit: 'd' },
			{ value: String(Math.floor((t % 86_400) / 3_600)).padStart(2, '0'), unit: 'h' },
			{ value: String(Math.floor((t % 3_600) / 60)).padStart(2, '0'), unit: 'm' },
			{ value: String(t % 60).padStart(2, '0'), unit: 's' }
		];
		// drop a leading 0d so it reads cleanly close to kickoff
		return parts[0].value === '0' ? parts.slice(1) : parts;
	}
</script>

<div class="mx-auto mt-20 flex max-w-md flex-col items-center text-center" role="status">
	<div class="relative mb-5">
		<span class="absolute inset-0 rounded-full bg-amber-500/10 blur-xl"></span>
		<Icon icon={unlocking ? 'mdi:lock-open-variant-outline' : 'mdi:lock-clock'} class="relative h-10 w-10 text-stone-500" />
	</div>
	{#if unlocking}
		<h2 class="text-xl font-semibold tracking-tight text-stone-100">The CTF is live</h2>
		<p class="mt-2 inline-flex items-center gap-2 text-sm text-stone-500">
			<Icon icon="mdi:loading" class="h-4 w-4 animate-spin" />
			Unlocking challenges…
		</p>
	{:else}
		<h2 class="text-xl font-semibold tracking-tight text-stone-100">The competition hasn't started yet</h2>
		<p class="mt-2 text-sm text-stone-500">
			{#if startText}Challenges unlock at <span class="text-stone-300">{startText}</span>.{:else}Challenges unlock the moment the CTF begins.{/if}
		</p>
		{#if startMs}
			<div class="mt-9 inline-flex items-center gap-3 rounded-full border border-stone-800 bg-stone-900/50 py-3 px-6">
				<span class="h-2 w-2 shrink-0 rounded-full bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.6)] animate-pulse"></span>
				<span class="whitespace-nowrap font-mono text-2xl font-medium tabular-nums text-stone-100">{#each units as u, i}{u.value}<span class="text-base text-stone-500">{u.unit}</span>{#if i < units.length - 1}<span class="px-1.5 text-stone-700">:</span>{/if}{/each}</span>
			</div>
			<p class="mt-3 text-xs text-stone-600">The board loads on its own at the start. No need to refresh.</p>
		{/if}
	{/if}
	<slot />
</div>
