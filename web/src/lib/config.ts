import { browser } from '$app/environment';
import { PUBLIC_API_URL } from '$env/static/public';

// API URL configuration
// Uses PUBLIC_API_URL from .env or docker build args
// Set PUBLIC_API_URL in:
//   - .env file for local dev
//   - Docker build args for production
//   - Environment variable at build time

function getApiUrl(): string {
	// SvelteKit static public env (set at build time)
	if (PUBLIC_API_URL && PUBLIC_API_URL !== '' && PUBLIC_API_URL !== 'undefined') {
		return PUBLIC_API_URL;
	}

	// Runtime fallback for development
	if (browser) {
		const protocol = window.location.protocol;
		const host = window.location.hostname;
		// Production: same origin (nginx proxies /api)
		if (host !== 'localhost' && host !== '127.0.0.1') {
			return `${protocol}//${host}`;
		}
	}

	// Local development fallback
	return 'http://localhost:8080';
}

export const API_BASE = getApiUrl();
