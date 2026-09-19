import { writable, derived } from 'svelte/store';
import { api, type PlatformInfoResponse } from '$api';

export const platformInfo = writable<PlatformInfoResponse | null>(null);

let started = false;

// loads /info once per session; the layout kicks it off so every page can read it.
export async function loadPlatformInfo() {
	if (started) return;
	started = true;
	try {
		platformInfo.set(await api.getPlatformInfo());
	} catch {
		started = false; // let a later mount retry
	}
}

// where "register" links point: the external registration site when configured,
// otherwise anvil's own signup page.
export const registerHref = derived(platformInfo, ($i) => $i?.register_url || '/register');
