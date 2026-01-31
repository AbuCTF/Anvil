import { browser } from '$app/environment';
import { auth } from '$stores/auth';
import { get } from 'svelte/store';
import { API_BASE } from '$lib/config';

interface ApiError {
	status: number;
	message: string;
	isAuthError: boolean;
}

class ApiClient {
	private baseUrl: string;

	constructor(baseUrl: string) {
		this.baseUrl = baseUrl;
	}

	private getAuthToken(): string | null {
		if (browser) {
			// First try localStorage (most reliable)
			const token = localStorage.getItem('accessToken');
			if (token) return token;
			
			// Fallback to store
			const authState = get(auth);
			return authState.accessToken;
		}
		return null;
	}

	private async request<T>(
		endpoint: string,
		options: RequestInit = {},
		requiresAuth = true
	): Promise<T> {
		const url = `${this.baseUrl}/api/v1${endpoint}`;
		
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			...(options.headers as Record<string, string> || {})
		};

		// Add auth token if available
		const token = this.getAuthToken();
		if (token) {
			headers['Authorization'] = `Bearer ${token}`;
		} else if (requiresAuth) {
			// If auth is required but no token, redirect to login
			if (browser) {
				window.location.href = '/login';
			}
			throw new Error('Authentication required');
		}

		try {
			const response = await fetch(url, {
				...options,
				headers
			});

			// Handle different response statuses
			if (response.status === 401) {
				// Token expired or invalid
				if (browser) {
					// Try to refresh token
					const refreshed = await this.tryRefreshToken();
					if (refreshed) {
						// Retry the request with new token
						const newToken = localStorage.getItem('accessToken');
						if (newToken) {
							headers['Authorization'] = `Bearer ${newToken}`;
							const retryResponse = await fetch(url, { ...options, headers });
							if (retryResponse.ok) {
								return retryResponse.json();
							}
						}
					}
					// Refresh failed - clear auth and redirect
					auth.clearAuth();
					window.location.href = '/login';
				}
				throw new Error('Session expired. Please login again.');
			}

			if (!response.ok) {
				const error = await response.json().catch(() => ({ error: 'Unknown error' }));
				throw new Error(error.error || error.message || `HTTP error ${response.status}`);
			}

			// Handle empty responses
			const contentType = response.headers.get('content-type');
			if (contentType && contentType.includes('application/json')) {
				return response.json();
			}
			return {} as T;
		} catch (error) {
			// Network errors - don't clear auth
			if (error instanceof TypeError && error.message.includes('fetch')) {
				throw new Error('Network error. Please check your connection.');
			}
			throw error;
		}
	}

	private async tryRefreshToken(): Promise<boolean> {
		const refreshToken = localStorage.getItem('refreshToken');
		if (!refreshToken) return false;

		try {
			const response = await fetch(`${this.baseUrl}/api/v1/auth/refresh`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ refresh_token: refreshToken })
			});

			if (response.ok) {
				const data = await response.json();
				if (data.access_token) {
					localStorage.setItem('accessToken', data.access_token);
					if (data.refresh_token) {
						localStorage.setItem('refreshToken', data.refresh_token);
					}
					// Update auth store
					auth.login(data.access_token, data.user, data.refresh_token);
					return true;
				}
			}
		} catch (error) {
			console.error('Token refresh failed:', error);
		}
		return false;
	}

	// Platform
	async getPlatformInfo() {
		return this.request<{ name: string; description: string }>('/info', {}, false);
	}

	// Auth
	async login(username: string, password: string) {
		return this.request<{
			access_token: string;
			refresh_token: string;
			user: any;
		}>('/auth/login', {
			method: 'POST',
			body: JSON.stringify({ username, password })
		}, false);
	}

	async register(username: string, email: string, password: string, inviteCode?: string) {
		return this.request<{
			access_token: string;
			refresh_token: string;
			user: any;
		}>('/auth/register', {
			method: 'POST',
			body: JSON.stringify({ username, email, password, invite_code: inviteCode })
		}, false);
	}

	async tokenAuth(token: string) {
		return this.request<{
			access_token: string;
			team: { token: string; team_name: string };
		}>('/auth/token', {
			method: 'POST',
			body: JSON.stringify({ token })
		}, false);
	}

	// Challenges - public endpoint
	async getChallenges(params?: { category?: string; difficulty?: string }) {
		const queryString = params
			? '?' + new URLSearchParams(params as Record<string, string>).toString()
			: '';
		return this.request<{ challenges: any[] }>(`/challenges${queryString}`, {}, false);
	}

	async getChallenge(slug: string) {
		return this.request<any>(`/challenges/${slug}`, {}, false);
	}

	async submitFlag(slug: string, flag: string) {
		return this.request<{
			correct: boolean;
			message: string;
			points_awarded: number;
		}>(`/challenges/${slug}/submit`, {
			method: 'POST',
			body: JSON.stringify({ flag })
		});
	}

	// Instances
	async getInstances() {
		return this.request<{ instances: any[] }>('/instances');
	}

	async createInstance(challengeId: string) {
		return this.request<any>('/instances', {
			method: 'POST',
			body: JSON.stringify({ challenge_id: challengeId })
		});
	}

	async extendInstance(instanceId: string) {
		return this.request<any>(`/instances/${instanceId}/extend`, {
			method: 'POST'
		});
	}

	async stopInstance(instanceId: string) {
		return this.request<any>(`/instances/${instanceId}/stop`, {
			method: 'POST'
		});
	}

	// VPN
	async getVPNConfig() {
		return this.request<{ config: string; assigned_ip: string }>('/vpn/config');
	}

	async generateVPNConfig() {
		return this.request<{ config: string; assigned_ip: string }>('/vpn/config', {
			method: 'POST'
		});
	}

	async getVPNStatus() {
		return this.request<any>('/vpn/status');
	}

	// User
	async getProfile() {
		return this.request<any>('/user/me');
	}

	async updateProfile(data: { display_name?: string; bio?: string }) {
		return this.request<any>('/user/me', {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async getUserStats() {
		return this.request<any>('/user/me/stats');
	}

	async getUserSolves() {
		return this.request<{ solves: any[] }>('/user/me/solves');
	}

	// Public stats - no auth required
	async getStats() {
		return this.request<any>('/stats', {}, false);
	}

	// Scoreboard - public endpoint
	async getScoreboard() {
		return this.request<{ leaderboard: any[]; total_users: number }>('/scoreboard', {}, false);
	}

	// Admin
	async getAdminStats() {
		return this.request<any>('/admin/stats');
	}

	async getAdminUsers() {
		return this.request<{ users: any[] }>('/admin/users');
	}

	async getAdminChallenges() {
		return this.request<{ challenges: any[] }>('/admin/challenges');
	}

	async createAdminUser(data: any) {
		return this.request<any>('/admin/users', {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async updateAdminUser(userId: string, data: any) {
		return this.request<any>(`/admin/users/${userId}`, {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async deleteAdminUser(userId: string) {
		return this.request<any>(`/admin/users/${userId}`, {
			method: 'DELETE'
		});
	}

	async createAdminChallenge(data: any) {
		return this.request<any>('/admin/challenges', {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async updateAdminChallenge(challengeId: string, data: any) {
		return this.request<any>(`/admin/challenges/${challengeId}`, {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async deleteAdminChallenge(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}`, {
			method: 'DELETE'
		});
	}

	async publishChallenge(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}/publish`, {
			method: 'POST'
		});
	}

	async unpublishChallenge(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}`, {
			method: 'PUT',
			body: JSON.stringify({ status: 'draft' })
		});
	}

	async deleteChallenge(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}`, {
			method: 'DELETE'
		});
	}

	async getAdminChallenge(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}`);
	}

	async getChallengeFlags(challengeId: string) {
		return this.request<any>(`/admin/challenges/${challengeId}/flags`);
	}

	// VM Templates
	async getVMTemplates() {
		return this.request<{ templates: any[] }>('/admin/vm-templates');
	}

	async createVMTemplate(data: any) {
		return this.request<any>('/admin/vm-templates', {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async uploadVMTemplate(formData: FormData) {
		const token = this.getAuthToken();
		const response = await fetch(`${this.baseUrl}/api/v1/admin/vm-templates/upload`, {
			method: 'POST',
			headers: token ? { 'Authorization': `Bearer ${token}` } : {},
			body: formData
		});
		if (!response.ok) {
			const error = await response.json().catch(() => ({ error: 'Upload failed' }));
			throw new Error(error.error || 'Upload failed');
		}
		return response.json();
	}

	async uploadOvaChallenge(formData: FormData, onProgress?: (progress: number) => void): Promise<any> {
		const token = this.getAuthToken();
		
		return new Promise((resolve, reject) => {
			const xhr = new XMLHttpRequest();
			
			xhr.upload.addEventListener('progress', (e) => {
				if (e.lengthComputable && onProgress) {
					const progress = Math.round((e.loaded / e.total) * 100);
					onProgress(progress);
				}
			});
			
			xhr.addEventListener('load', () => {
				if (xhr.status >= 200 && xhr.status < 300) {
					try {
						resolve(JSON.parse(xhr.responseText));
					} catch {
						resolve({});
					}
				} else {
					try {
						const error = JSON.parse(xhr.responseText);
						reject(new Error(error.error || 'Upload failed'));
					} catch {
						reject(new Error('Upload failed'));
					}
				}
			});
			
			xhr.addEventListener('error', () => {
				reject(new Error('Network error during upload'));
			});
			
			xhr.open('POST', `${this.baseUrl}/api/v1/admin/challenges/ova`);
			if (token) {
				xhr.setRequestHeader('Authorization', `Bearer ${token}`);
			}
			xhr.send(formData);
		});
	}
}

export const api = new ApiClient(API_BASE);
