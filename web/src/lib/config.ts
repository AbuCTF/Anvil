import { browser } from '$app/environment';

// API URL configuration
// In browser: uses window location to determine API URL
// Priority: 1. Explicit PUBLIC_API_URL env var at build time
//          2. Same host as frontend on port 8080
//          3. localhost:8080 fallback

function getApiUrl(): string {
	// Build-time env var (if set during docker build)
	const buildTimeUrl = import.meta.env.PUBLIC_API_URL;
	if (buildTimeUrl && buildTimeUrl !== 'undefined') {
		return buildTimeUrl;
	}

	// Runtime detection in browser
	if (browser) {
		const host = window.location.hostname;
		// If accessing via IP or domain, use same host with API port
		if (host !== 'localhost' && host !== '127.0.0.1') {
			return `http://${host}:8080`;
		}
	}

	// Default fallback for local development
	return 'http://localhost:8080';
}

export const API_BASE = getApiUrl();
