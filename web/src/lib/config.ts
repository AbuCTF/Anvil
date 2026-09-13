import { browser } from '$app/environment';
import { env } from '$env/dynamic/public';

// Read via dynamic env so a missing PUBLIC_API_URL is `undefined` rather than a
// hard module-load failure, and can be set at runtime without a rebuild.
const PUBLIC_API_URL = env.PUBLIC_API_URL;

// API URL configuration
// Uses PUBLIC_API_URL from the runtime environment when provided.
// Set PUBLIC_API_URL in:
//   - .env file for local dev
//   - the web container environment in production

function getApiUrl(): string {
	// SvelteKit dynamic public env (set at runtime)
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
