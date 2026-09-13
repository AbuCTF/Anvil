<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EventPhase } from '$api';
	import { instantTitle } from '$lib/time';

	export let className = '';

	interface EventWindow {
		startAt: number;
		endAt: number;
	}

	let eventWindow: EventWindow | null = null;
	let serverOffset = 0;
	let phase: EventPhase = 'scheduled';
	let remainingSeconds = 0;
	let progress = 0;

	function formatDuration(totalSeconds: number): string {
		const seconds = Math.max(0, Math.floor(totalSeconds));
		const days = Math.floor(seconds / 86_400);
		const hours = Math.floor((seconds % 86_400) / 3_600);
		const minutes = Math.floor((seconds % 3_600) / 60);
		const remainder = seconds % 60;
		const clock = [hours, minutes, remainder].map((value) => String(value).padStart(2, '0')).join(':');
		return days > 0 ? `${days}d ${clock}` : clock;
	}

	function updateClock() {
		if (!eventWindow) return;
		const now = Date.now() + serverOffset;
		if (now < eventWindow.startAt) {
			phase = 'scheduled';
			remainingSeconds = Math.ceil((eventWindow.startAt - now) / 1000);
			progress = 0;
		} else if (now < eventWindow.endAt) {
			phase = 'live';
			remainingSeconds = Math.ceil((eventWindow.endAt - now) / 1000);
			progress = Math.min(100, Math.max(0, ((now - eventWindow.startAt) / (eventWindow.endAt - eventWindow.startAt)) * 100));
		} else {
			phase = 'ended';
			remainingSeconds = 0;
			progress = 100;
		}
	}

	$: phaseLabel = phase === 'scheduled' ? 'Starts' : phase === 'live' ? 'Live' : 'CTF';
	$: clockText = phase === 'ended' ? 'Ended' : formatDuration(remainingSeconds);
	$: title = eventWindow
		? phase === 'scheduled'
			? `Starts ${instantTitle(eventWindow.startAt)}`
			: phase === 'live'
				? `Live · ends ${instantTitle(eventWindow.endAt)}`
				: `Ended ${instantTitle(eventWindow.endAt)}`
		: '';
	$: accessibleLabel = phase === 'scheduled'
		? `CTF starts in ${clockText}`
		: phase === 'live'
			? `CTF is live, ${clockText} remaining`
			: 'CTF has ended';

	onMount(() => {
		let active = true;
		let interval: number | undefined;

		void (async () => {
			const requestedAt = Date.now();
			try {
				const info = await api.getPlatformInfo();
				const receivedAt = Date.now();
				if (!active || !info.event) return;

				const serverTime = Date.parse(info.server_time);
				const startAt = Date.parse(info.event.start_at);
				const endAt = Date.parse(info.event.end_at);
				if (![serverTime, startAt, endAt].every(Number.isFinite) || endAt <= startAt) return;

				serverOffset = serverTime - (requestedAt + receivedAt) / 2;
				eventWindow = { startAt, endAt };
				updateClock();
				interval = window.setInterval(updateClock, 1000);
			} catch {
				// The navigation remains complete without event timing.
			}
		})();

		return () => {
			active = false;
			if (interval !== undefined) window.clearInterval(interval);
		};
	});
</script>

{#if eventWindow}
	<div
		class="relative inline-flex h-8 items-center gap-2 overflow-hidden rounded-full border border-stone-800 bg-stone-900/55 px-2.5 text-stone-300 {className}"
		title={title}
		aria-label={accessibleLabel}
	>
		<span
			class="h-1.5 w-1.5 shrink-0 rounded-full {phase === 'live' ? 'bg-amber-500 motion-safe:animate-pulse' : phase === 'scheduled' ? 'bg-info' : 'bg-stone-600'}"
			aria-hidden="true"
		></span>
		<span class="metadata-label optical-label hidden text-stone-500 sm:inline">{phaseLabel}</span>
		<span class="optical-label whitespace-nowrap text-[0.68rem] font-medium leading-none tabular-nums text-stone-200">{clockText}</span>
		{#if phase === 'live'}
			<span class="absolute inset-x-0 bottom-0 h-px bg-stone-800" aria-hidden="true">
				<span class="block h-full bg-amber-500/70" style={`width: ${progress}%`}></span>
			</span>
		{/if}
	</div>
{/if}
