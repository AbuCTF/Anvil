import { browser } from '$app/environment';
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const ssr = false;

export const load: PageLoad = async () => {
	if (browser) {
		// Check if user is authenticated
		const token = localStorage.getItem('accessToken');
		if (!token) {
			throw redirect(302, '/login');
		}
		
		// We can't easily check the role here without making an API call
		// The admin page component will handle the role check after auth.checkAuth()
		// If user is not admin, it will redirect from the onMount
	}
	
	return {};
};
