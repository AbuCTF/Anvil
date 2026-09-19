<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EventPhase } from '$api';
	import { formatLocalDateTimeWithZone, instantTitle } from '$lib/time';

	export let className = '';

	interface EventWindow {
		startAt: number;
		endAt: number;
		visibleUntil: number;
	}

	let root: HTMLDivElement;
	let eventWindow: EventWindow | null = null;
	let serverOffset = 0;
	let phase: EventPhase = 'scheduled';
	let remainingSeconds = 0;
	let open = false;
	let clockTimer: number | undefined;

	function formatDuration(totalSeconds: number): string {
		const seconds = Math.max(0, Math.floor(totalSeconds));
		const days = Math.floor(seconds / 86_400);
		const hours = Math.floor((seconds % 86_400) / 3_600);
		const minutes = Math.floor((seconds % 3_600) / 60);
		const remainder = seconds % 60;
		const clock = [hours, minutes, remainder].map((value) => String(value).padStart(2, '0')).join(':');
		return days > 0 ? `${days}d ${clock}` : clock;
	}

	function formatWindowDuration(startAt: number, endAt: number): string {
		let minutes = Math.max(0, Math.round((endAt - startAt) / 60_000));
		const days = Math.floor(minutes / 1_440);
		minutes %= 1_440;
		const hours = Math.floor(minutes / 60);
		minutes %= 60;
		const parts: string[] = [];
		if (days) parts.push(`${days} ${days === 1 ? 'day' : 'days'}`);
		if (hours) parts.push(`${hours} ${hours === 1 ? 'hour' : 'hours'}`);
		if (minutes || parts.length === 0) parts.push(`${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`);
		return parts.join(' ');
	}

	function updateClock() {
		if (!eventWindow) return;
		const now = Date.now() + serverOffset;
		if (now >= eventWindow.visibleUntil) {
			eventWindow = null;
			open = false;
			if (clockTimer !== undefined) window.clearInterval(clockTimer);
			clockTimer = undefined;
			return;
		}
		if (now < eventWindow.startAt) {
			phase = 'scheduled';
			remainingSeconds = Math.ceil((eventWindow.startAt - now) / 1000);
		} else if (now < eventWindow.endAt) {
			phase = 'live';
			remainingSeconds = Math.ceil((eventWindow.endAt - now) / 1000);
		} else {
			phase = 'ended';
			remainingSeconds = 0;
		}
	}

	$: phaseLabel = phase === 'scheduled' ? 'Starts' : phase === 'live' ? 'Live' : 'CTF';
	$: clockText = phase === 'ended' ? 'Ended' : formatDuration(remainingSeconds);
	$: statusText = phase === 'scheduled' ? 'Scheduled' : phase === 'live' ? 'In progress' : 'Finished';
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
	$: startText = eventWindow ? formatLocalDateTimeWithZone(eventWindow.startAt) : '';
	$: endText = eventWindow ? formatLocalDateTimeWithZone(eventWindow.endAt) : '';
	$: durationText = eventWindow ? formatWindowDuration(eventWindow.startAt, eventWindow.endAt) : '';

	function handleDocumentPointerDown(event: PointerEvent) {
		if (open && event.target instanceof Node && !root?.contains(event.target)) open = false;
	}

	function handleDocumentKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') open = false;
	}

	onMount(() => {
		let active = true;
		document.addEventListener('pointerdown', handleDocumentPointerDown);
		document.addEventListener('keydown', handleDocumentKeydown);

		void (async () => {
			const requestedAt = Date.now();
			try {
				const info = await api.getPlatformInfo();
				const receivedAt = Date.now();
				if (!active || !info.event) return;

				const serverTime = Date.parse(info.server_time);
				const startAt = Date.parse(info.event.start_at);
				const endAt = Date.parse(info.event.end_at);
				const visibleUntil = Date.parse(info.event.visible_until);
				if (![serverTime, startAt, endAt, visibleUntil].every(Number.isFinite) || endAt <= startAt || visibleUntil <= endAt) return;

				serverOffset = serverTime - (requestedAt + receivedAt) / 2;
				eventWindow = { startAt, endAt, visibleUntil };
				updateClock();
				if (eventWindow) clockTimer = window.setInterval(updateClock, 1000);
			} catch {
				// the navigation remains complete without event timing.
			}
		})();

		return () => {
			active = false;
			if (clockTimer !== undefined) window.clearInterval(clockTimer);
			document.removeEventListener('pointerdown', handleDocumentPointerDown);
			document.removeEventListener('keydown', handleDocumentKeydown);
		};
	});
</script>

{#if eventWindow}
	<div bind:this={root} class="relative inline-flex {className}">
		<button
			type="button"
			class="relative inline-flex h-8 items-center gap-2 overflow-hidden rounded-full border border-stone-800 bg-stone-900/55 px-2.5 text-stone-300 transition-colors hover:border-stone-700 hover:bg-stone-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-500/50"
			title={title}
			aria-label={`${accessibleLabel}. Show event details`}
			aria-haspopup="dialog"
			aria-expanded={open}
			aria-controls="event-clock-details"
			on:click={() => (open = !open)}
		>
			<span
				class="relative top-[0.5px] block h-2 w-2 flex-none rounded-full {phase === 'live' ? 'live-dot bg-amber-500' : phase === 'scheduled' ? 'bg-info' : 'bg-stone-600'}"
				aria-hidden="true"
			></span>
			<span class="metadata-label optical-label hidden text-stone-500 2xl:inline">{phaseLabel}</span>
			<span class="clock-value whitespace-nowrap text-[0.68rem] font-medium leading-none tabular-nums text-stone-200">{clockText}</span>
		</button>

		{#if open}
			<div
				id="event-clock-details"
				role="dialog"
				aria-label="CTF schedule"
				class="fixed inset-x-4 top-[4.5rem] z-[70] rounded-lg border border-stone-800 bg-stone-950 p-4 shadow-2xl shadow-black/30 sm:absolute sm:inset-x-auto sm:right-0 sm:top-full sm:mt-2 sm:w-72"
			>
				<div class="mb-3 flex items-center justify-between gap-3">
					<p class="text-sm font-medium text-stone-200">CTF schedule</p>
					<span class="inline-flex items-center gap-1.5 text-[0.65rem] text-stone-500">
						<span class="h-1.5 w-1.5 rounded-full {phase === 'live' ? 'bg-amber-500' : phase === 'scheduled' ? 'bg-info' : 'bg-stone-600'}" aria-hidden="true"></span>
						<span class="optical-label">{statusText}</span>
					</span>
				</div>
				<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2.5 text-xs">
					<dt class="metadata-label text-stone-600">Starts</dt>
					<dd class="text-right text-stone-300 tabular-nums" title={instantTitle(eventWindow.startAt)}>
						<time datetime={new Date(eventWindow.startAt).toISOString()}>{startText}</time>
					</dd>
					<dt class="metadata-label text-stone-600">Ends</dt>
					<dd class="text-right text-stone-300 tabular-nums" title={instantTitle(eventWindow.endAt)}>
						<time datetime={new Date(eventWindow.endAt).toISOString()}>{endText}</time>
					</dd>
					<dt class="metadata-label text-stone-600">Duration</dt>
					<dd class="text-right text-stone-300">{durationText}</dd>
				</dl>
			</div>
		{/if}
	</div>
{/if}

<style>
	.clock-value {
		position: relative;
		top: 1px;
	}

	.live-dot {
		animation: live-blink 1.8s ease-in-out infinite;
	}

	@keyframes live-blink {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.45;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.live-dot {
			animation: none;
		}
	}
</style>
