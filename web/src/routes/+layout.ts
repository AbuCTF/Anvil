import { browser } from '$app/environment';
import type { LayoutLoad } from './$types';

// SSR on here for a fast first paint, then hydrate client-side
export const ssr = true;
export const prerender = false;

export const load: LayoutLoad = () => {
	if (!browser) return { user: null, isAuthenticated: false };

	try {
		const user = JSON.parse(localStorage.getItem('user') || 'null');
		return { user, isAuthenticated: Boolean(user && localStorage.getItem('accessToken')) };
	} catch {
		return { user: null, isAuthenticated: false };
	}
};
