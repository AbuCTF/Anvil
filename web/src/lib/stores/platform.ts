import { writable, derived, readable } from 'svelte/store';
import { browser } from '$app/environment';
import { api, type EventPhase, type PlatformInfoResponse } from '$api';

export const platformInfo = writable<PlatformInfoResponse | null>(null);

// server_time - local clock at /info receipt, so countdowns ignore local clock skew.
const serverOffset = writable(0);

let started = false;

// loads /info once per session; the layout kicks it off so every page can read it.
export async function loadPlatformInfo() {
	if (started) return;
	started = true;
	try {
		const info = await api.getPlatformInfo();
		const offset = Date.parse(info.server_time) - Date.now();
		serverOffset.set(Number.isNaN(offset) ? 0 : offset);
		platformInfo.set(info);
	} catch {
		started = false; // let a later mount retry
	}
}

// where "register" links point: the external registration site when configured,
// otherwise anvil's own signup page.
export const registerHref = derived(platformInfo, ($i) => $i?.register_url || '/register');

// whether the signed-in user is on a team (teams mode); null = unknown.
export const hasTeam = writable<boolean | null>(null);

const tick = readable(Date.now(), (set) => {
	if (!browser) return;
	const t = setInterval(() => set(Date.now()), 1000);
	return () => clearInterval(t);
});

// event phase off a ticking server-corrected clock, so pages flip to live at
// go-live without a reload. no parseable start => /info's static phase.
export const eventClock = derived([platformInfo, serverOffset, tick], ([$info, $offset, $now]) => {
	const start = Date.parse($info?.event?.start_at ?? '');
	const end = Date.parse($info?.event?.end_at ?? '');
	const t = $now + $offset;
	if (Number.isNaN(start)) {
		return { phase: ($info?.event?.phase ?? null) as EventPhase | null, startMs: null as number | null, untilStart: 0 };
	}
	const phase: EventPhase = t < start ? 'scheduled' : !Number.isNaN(end) && t >= end ? 'ended' : 'live';
	return { phase: phase as EventPhase | null, startMs: start as number | null, untilStart: Math.max(0, start - t) };
});

// go-live refetch delay: spread over 1–10 s (retries 5–15 s) so a few thousand
// open tabs don't hit the api in the same second.
export const kickoffDelay = (retry = false) => (retry ? 5000 : 1000) + Math.random() * (retry ? 10_000 : 9000);
