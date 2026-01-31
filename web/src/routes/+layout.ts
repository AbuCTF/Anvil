import { browser } from '$app/environment';
import type { LayoutLoad } from './$types';

// Use environment variable, fallback to localhost for local dev
const API_BASE = import.meta.env.PUBLIC_API_URL || 'http://localhost:8080';

export const load: LayoutLoad = async ({ fetch }) => {
	if (browser) {
		const token = localStorage.getItem('accessToken');
		
		if (token) {
			try {
				const response = await fetch(`${API_BASE}/api/v1/user/me`, {
					headers: {
						'Authorization': `Bearer ${token}`
					}
				});
				
				if (response.ok) {
					const user = await response.json();
					return { user, isAuthenticated: true };
				} else if (response.status === 401) {
					// Token expired - try refresh
					const refreshToken = localStorage.getItem('refreshToken');
					if (refreshToken) {
						try {
							const refreshResponse = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
								method: 'POST',
								headers: { 'Content-Type': 'application/json' },
								body: JSON.stringify({ refresh_token: refreshToken })
							});
							
							if (refreshResponse.ok) {
								const refreshData = await refreshResponse.json();
								localStorage.setItem('accessToken', refreshData.access_token);
								if (refreshData.refresh_token) {
									localStorage.setItem('refreshToken', refreshData.refresh_token);
								}
								return { user: refreshData.user, isAuthenticated: true };
							}
						} catch (refreshError) {
							console.error('Token refresh failed:', refreshError);
						}
					}
					// Clear invalid tokens
					localStorage.removeItem('accessToken');
					localStorage.removeItem('refreshToken');
				}
			} catch (error) {
				console.error('Failed to load user:', error);
				// Network error - don't clear tokens, user might be offline
			}
		}
	}
	
	return { user: null, isAuthenticated: false };
};
