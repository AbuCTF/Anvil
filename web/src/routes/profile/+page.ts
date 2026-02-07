import { browser } from '$app/environment';
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const ssr = false;

export const load: PageLoad = async () => {
	if (browser) {
		const token = localStorage.getItem('accessToken');
		if (!token) {
			throw redirect(302, '/login');
		}
	}
	return {};
};
