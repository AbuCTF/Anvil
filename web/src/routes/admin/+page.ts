import { browser } from '$app/environment';
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const ssr = false;

export const load: PageLoad = async () => {
	// Auth check happens in layout, component will handle role-based redirect
	return {};
};
