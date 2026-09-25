import { writable, get } from 'svelte/store';
import { browser } from '$app/environment';
import { API_BASE } from '$lib/config';

interface User {
	id: string;
	username: string;
	email?: string;
	display_name?: string;
	role: string;
	total_score: number;
	rank: number;
}

interface AuthState {
	isAuthenticated: boolean;
	user: User | null;
	accessToken: string | null;
	isLoading: boolean;
	lastChecked: number | null;
}

const AUTH_CHECK_INTERVAL = 60000; // re-check auth every 60 seconds max
const RANK_CHECK_INTERVAL = 60000; // revalidate at most once per visible minute
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
	let rankRefreshPromise: Promise<void> | null = null;
	let rankETag = '';
	let lastRankChecked = 0;
	let rankGeneration = 0;
	let authGeneration = 0;

	const resetRankRevalidation = (checkedAt = 0) => {
		rankGeneration++;
		rankRefreshPromise = null;
		rankETag = '';
		lastRankChecked = checkedAt;
	};

	const storeRank = (rank: number) => {
		if (!Number.isInteger(rank) || rank < 0) return;
		update((state) => {
			if (!state.user || state.user.rank === rank) return state;
			const user = { ...state.user, rank };
			if (browser) localStorage.setItem('user', JSON.stringify(user));
			return { ...state, user };
		});
	};

	const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

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
			resetRankRevalidation(Date.now());
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
			resetRankRevalidation();
			if (browser) {
				localStorage.removeItem('accessToken');
				localStorage.removeItem('refreshToken');
				localStorage.removeItem('user');
			}
			set(initialState);
			if (browser && redirect) {
				window.location.href = '/login';
			}
		},

		// initialize auth from stored token - call once on app load
		initialize: async () => {
			if (!browser) return;
			
			const currentState = get({ subscribe });
			if (currentState.isLoading) return; // prevent concurrent checks
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
					resetRankRevalidation(Date.now());
					localStorage.setItem('user', JSON.stringify(user));
					set({
						isAuthenticated: true,
						user,
						accessToken: token,
						isLoading: false,
						lastChecked: Date.now()
					});
				} else if (response.status === 401) {
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
							resetRankRevalidation(Date.now());
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
					localStorage.removeItem('accessToken');
					localStorage.removeItem('refreshToken');
					localStorage.removeItem('user');
					set(initialState);
				} else {
					// server error (5xx) - don't clear auth, keep trying
					console.error('Auth check failed with status:', response.status);
					update(s => ({ ...s, isLoading: false }));
				}
			} catch (error) {
				if (generation !== authGeneration) return;
				// network error - don't clear auth, user might be offline
				console.error('Auth check network error:', error);
				update(s => ({ 
					...s, 
					isLoading: false,
					// if we had a token, assume still authenticated (offline mode)
					isAuthenticated: !!token,
					accessToken: token
				}));
			}
		},

		// check auth - debounced by default; score-changing actions can force a refresh
		checkAuth: async (force = false) => {
			if (!browser) return;

			const currentState = get({ subscribe });
			const now = Date.now();
			const generation = authGeneration;

			if (currentState.isLoading) return;

			if (!force && currentState.lastChecked && (now - currentState.lastChecked) < AUTH_CHECK_INTERVAL) {
				return;
			}

			const token = localStorage.getItem('accessToken');
			
			if (!token) {
				if (currentState.isAuthenticated) {
					set(initialState);
				}
				return;
			}

			// token exists but state doesn't reflect it - sync from storage
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
					resetRankRevalidation(Date.now());
					set({
						isAuthenticated: true,
						user,
						accessToken: token,
						isLoading: false,
						lastChecked: now
					});
				} else if (response.status === 401) {
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
							resetRankRevalidation(Date.now());
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
					localStorage.removeItem('accessToken');
					localStorage.removeItem('refreshToken');
					localStorage.removeItem('user');
					set(initialState);
				} else {
					// server error - don't clear auth
					console.error('Auth check failed with status:', response.status);
					update(s => ({ ...s, isLoading: false, lastChecked: now }));
				}
			} catch (error) {
				if (generation !== authGeneration) return;
				// network error - keep existing auth state
				console.error('Auth check error:', error);
				update(s => ({ ...s, isLoading: false }));
			}
		},

		// revalidate only the header rank when a dormant tab becomes visible.
		// conditional requests return no body when the rank has not changed.
		refreshRank: async (force = false) => {
			if (!browser || document.hidden) return;
			const state = get({ subscribe });
			if (!state.isAuthenticated || !state.user || state.isLoading) return;
			if (!force && Date.now() - lastRankChecked < RANK_CHECK_INTERVAL) return;
			if (rankRefreshPromise) return rankRefreshPromise;

			const generation = authGeneration;
			const currentRankGeneration = rankGeneration;
			const isCurrent = () => generation === authGeneration && currentRankGeneration === rankGeneration;
			const currentPromise = (async () => {
				const request = (token: string) => {
					const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
					if (rankETag) headers['If-None-Match'] = rankETag;
					return fetchWithRetry(`${API_BASE}/api/v1/user/me/rank`, {
						headers
					});
				};

				try {
					let token = localStorage.getItem('accessToken');
					if (!token) return;
					let response = await request(token);
					if (!isCurrent()) return;

					if (response.status === 401) {
						const refreshedToken = await refreshAccessToken();
						if (!refreshedToken || !isCurrent()) return;
						token = refreshedToken;
						response = await request(token);
						if (!isCurrent()) return;
					}

					if (response.status === 304) {
						rankETag = response.headers.get('etag') || rankETag;
						lastRankChecked = Date.now();
						return;
					}
					if (!response.ok) return;

					const payload: unknown = await response.json();
					if (!isCurrent() || !payload || typeof payload !== 'object') return;
					const rank = (payload as { rank?: unknown }).rank;
					if (typeof rank !== 'number' || !Number.isInteger(rank) || rank < 0) return;
					rankETag = response.headers.get('etag') || '';
					lastRankChecked = Date.now();
					storeRank(rank);
				} catch (error) {
					if (isCurrent()) console.error('Rank refresh failed:', error);
				}
			})();
			rankRefreshPromise = currentPromise;
			try {
				await currentPromise;
			} finally {
				if (rankRefreshPromise === currentPromise) rankRefreshPromise = null;
			}
		},

		updateRank: (rank: number) => {
			resetRankRevalidation(Date.now());
			storeRank(rank);
		},

		updateUser: (user: User) => {
			update((state) => ({
				...state,
				user
			}));
		},

		// force clear auth (for explicit logout or security reasons)
		clearAuth: () => {
			authGeneration++;
			refreshPromise = null;
			resetRankRevalidation();
			if (browser) {
				localStorage.removeItem('accessToken');
				localStorage.removeItem('refreshToken');
				localStorage.removeItem('user');
			}
			set(initialState);
		},

		getToken: (): string | null => {
			if (browser) {
				return localStorage.getItem('accessToken');
			}
			return get({ subscribe }).accessToken;
		}
	};
}

export const auth = createAuthStore();
