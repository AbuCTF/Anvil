import { browser } from '$app/environment';
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const ssr = false;

export const load: PageLoad = async ({ parent }) => {
	if (browser) {
		const token = localStorage.getItem('accessToken');
		if (!token) {
			throw redirect(302, '/login');
		}
		
		// Get user from parent layout
		const { user } = await parent();
		if (user && user.role !== 'admin') {
			throw redirect(302, '/');
		}
	}
	return {};
};
