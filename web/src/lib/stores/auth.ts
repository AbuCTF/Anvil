import { writable, get } from 'svelte/store';
import { browser } from '$app/environment';
import { API_BASE } from '$lib/config';

interface User {
	id: string;
	username: string;
	email?: string;
	role: string;
	totalScore: number;
}

interface AuthState {
	isAuthenticated: boolean;
	user: User | null;
	accessToken: string | null;
	isLoading: boolean;
	lastChecked: number | null;
}

const AUTH_CHECK_INTERVAL = 60000; // Re-check auth every 60 seconds max
const MAX_RETRIES = 2;
const RETRY_DELAY = 1000;

function createAuthStore() {
	const initialState: AuthState = {
		isAuthenticated: false,
		user: null,
		accessToken: null,
		isLoading: false,
		lastChecked: null
	};

	const { subscribe, set, update } = writable<AuthState>(initialState);
	let refreshPromise: Promise<string | null> | null = null;
	let authGeneration = 0;

	// Helper to delay execution
	const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

	// Fetch with retry logic for network errors
	const fetchWithRetry = async (url: string, options: RequestInit, retries = MAX_RETRIES): Promise<Response> => {
		try {
			return await fetch(url, options);
		} catch (error) {
			if (retries > 0) {
				await delay(RETRY_DELAY);
				return fetchWithRetry(url, options, retries - 1);
			}
			throw error;
		}
	};

	const refreshAccessToken = async (): Promise<string | null> => {
		if (!browser) return null;
		if (refreshPromise) return refreshPromise;

		const generation = authGeneration;
		const currentPromise = (async () => {
			const refreshToken = localStorage.getItem('refreshToken');
			if (!refreshToken) return null;

			try {
				const response = await fetchWithRetry(`${API_BASE}/api/v1/auth/refresh`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ refresh_token: refreshToken })
				});
				if (!response.ok) return null;

				const data = await response.json();
				if (!data.access_token) return null;
				if (generation !== authGeneration || localStorage.getItem('refreshToken') !== refreshToken) {
					return null;
				}

				localStorage.setItem('accessToken', data.access_token);
				if (data.refresh_token) localStorage.setItem('refreshToken', data.refresh_token);
				update((state) => ({ ...state, accessToken: data.access_token, lastChecked: null }));
				return data.access_token as string;
			} catch (error) {
				console.error('Token refresh failed:', error);
				return null;
			}
		})();
		refreshPromise = currentPromise;

		try {
			return await currentPromise;
		} finally {
			if (refreshPromise === currentPromise) refreshPromise = null;
		}
	};

	return {
		subscribe,

		login: (accessToken: string, user: User, refreshToken?: string) => {
			authGeneration++;
			refreshPromise = null;
			if (browser) {
				localStorage.setItem('accessToken', accessToken);
				localStorage.setItem('user', JSON.stringify(user));
				if (refreshToken) {
					localStorage.setItem('refreshToken', refreshToken);
				}
			}
			set({
				isAuthenticated: true,
				user,
				accessToken,
				isLoading: false,
				lastChecked: Date.now()
			});
		},

		refreshAccessToken,

		logout: (redirect = true) => {
			authGeneration++;
			refreshPromise = null;
			if (browser) {
				localStorage.removeItem('accessToken');
				localStorage.removeItem('refreshToken');
				localStorage.removeItem('user');
			}
			set(initialState);
			// Redirect to login after logout
			if (browser && redirect) {
				window.location.href = '/login';
			}
		},

		// Initialize auth from stored token - call once on app load
		initialize: async () => {
			if (!browser) return;
			
			const currentState = get({ subscribe });
			if (currentState.isLoading) return; // Prevent concurrent checks
			const generation = authGeneration;

			const token = localStorage.getItem('accessToken');
			if (!token) {
				set(initialState);
				return;
			}

			update(s => ({ ...s, isLoading: true }));

			try {
				const response = await fetchWithRetry(`${API_BASE}/api/v1/user/me`, {
					headers: { 'Authorization': `Bearer ${token}` }
				});
				if (generation !== authGeneration) return;

				if (response.ok) {
					const user = await response.json();
					if (generation !== authGeneration) return;
					localStorage.setItem('user', JSON.stringify(user));
					set({
						isAuthenticated: true,
						user,
						accessToken: token,
						isLoading: false,
						lastChecked: Date.now()
					});
				} else if (response.status === 401) {
					// Token expired - try refresh
					const newToken = await refreshAccessToken();
					if (generation !== authGeneration) return;
					if (newToken) {
						const retryResponse = await fetchWithRetry(`${API_BASE}/api/v1/user/me`, {
							headers: { 'Authorization': `Bearer ${newToken}` }
						});
						if (generation !== authGeneration) return;
						if (retryResponse.ok) {
							const user = await retryResponse.json();
							if (generation !== authGeneration) return;
							localStorage.setItem('user', JSON.stringify(user));
							set({
								isAuthenticated: true,
								user,
								accessToken: newToken,
								isLoading: false,
								lastChecked: Date.now()
							});
							return;
						}
					}
					// Refresh failed - clear auth
					localStorage.removeItem('accessToken');
					localStorage.removeItem('refreshToken');
					localStorage.removeItem('user');
					set(initialState);
				} else {
					// Server error (5xx) - don't clear auth, keep trying
					console.error('Auth check failed with status:', response.status);
					update(s => ({ ...s, isLoading: false }));
				}
			} catch (error) {
				if (generation !== authGeneration) return;
				// Network error - don't clear auth, user might be offline
				console.error('Auth check network error:', error);
				// Keep existing token but mark as unchecked
				update(s => ({ 
					...s, 
					isLoading: false,
					// If we had a token, assume still authenticated (offline mode)
					isAuthenticated: !!token,
					accessToken: token
				}));
			}
		},

		// Check auth - with debouncing to prevent excessive API calls
		checkAuth: async () => {
			if (!browser) return;

			const currentState = get({ subscribe });
			const now = Date.now();
			const generation = authGeneration;

			// Skip if already loading
			if (currentState.isLoading) return;

			// Skip if recently checked (within interval)
			if (currentState.lastChecked && (now - currentState.lastChecked) < AUTH_CHECK_INTERVAL) {
				return;
			}

			const token = localStorage.getItem('accessToken');
			
			// No token - ensure state is cleared
			if (!token) {
				if (currentState.isAuthenticated) {
					set(initialState);
				}
				return;
			}

			// Token exists but state doesn't reflect it - sync from storage
			if (!currentState.accessToken && token) {
				update(s => ({ ...s, accessToken: token, isLoading: true }));
			} else {
				update(s => ({ ...s, isLoading: true }));
			}

			try {
				const response = await fetchWithRetry(`${API_BASE}/api/v1/user/me`, {
					headers: { 'Authorization': `Bearer ${token}` }
				});
				if (generation !== authGeneration) return;

				if (response.ok) {
					const user = await response.json();
					if (generation !== authGeneration) return;
					set({
						isAuthenticated: true,
						user,
						accessToken: token,
						isLoading: false,
						lastChecked: now
					});
				} else if (response.status === 401) {
					// Token invalid - try refresh
					const newToken = await refreshAccessToken();
					if (generation !== authGeneration) return;
					if (newToken) {
						const retryResponse = await fetchWithRetry(`${API_BASE}/api/v1/user/me`, {
							headers: { 'Authorization': `Bearer ${newToken}` }
						});
						if (generation !== authGeneration) return;
						if (retryResponse.ok) {
							const user = await retryResponse.json();
							if (generation !== authGeneration) return;
							localStorage.setItem('user', JSON.stringify(user));
							set({
								isAuthenticated: true,
								user,
								accessToken: newToken,
								isLoading: false,
								lastChecked: Date.now()
							});
							return;
						}
					}
					// Refresh failed - clear everything
					localStorage.removeItem('accessToken');
					localStorage.removeItem('refreshToken');
					localStorage.removeItem('user');
					set(initialState);
				} else {
					// Server error - don't clear auth
					console.error('Auth check failed with status:', response.status);
					update(s => ({ ...s, isLoading: false, lastChecked: now }));
				}
			} catch (error) {
				if (generation !== authGeneration) return;
				// Network error - keep existing auth state
				console.error('Auth check error:', error);
				update(s => ({ ...s, isLoading: false }));
			}
		},

		updateUser: (user: User) => {
			update((state) => ({
				...state,
				user
			}));
		},

		// Force clear auth (for explicit logout or security reasons)
		clearAuth: () => {
			authGeneration++;
			refreshPromise = null;
			if (browser) {
				localStorage.removeItem('accessToken');
				localStorage.removeItem('refreshToken');
				localStorage.removeItem('user');
			}
			set(initialState);
		},

		// Get current token (for API calls)
		getToken: (): string | null => {
			if (browser) {
				return localStorage.getItem('accessToken');
			}
			return get({ subscribe }).accessToken;
		}
	};
}

export const auth = createAuthStore();
