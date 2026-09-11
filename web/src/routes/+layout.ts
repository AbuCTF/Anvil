import { browser } from '$app/environment';
import type { LayoutLoad } from './$types';

// Enable SSR for faster initial page load, hydrate client-side
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
