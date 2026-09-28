import { browser } from '$app/environment';
import { env } from '$env/dynamic/public';

// read via dynamic env so a missing PUBLIC_API_URL is `undefined` rather than a
// hard module-load failure, and can be set at runtime without a rebuild.
const PUBLIC_API_URL = env.PUBLIC_API_URL;
const PUBLIC_UPLOAD_URL = env.PUBLIC_UPLOAD_URL;

function getApiUrl(): string {
	if (PUBLIC_API_URL && PUBLIC_API_URL !== '' && PUBLIC_API_URL !== 'undefined') {
		return PUBLIC_API_URL;
	}

	if (browser) {
		const protocol = window.location.protocol;
		const host = window.location.hostname;
		// production: same origin (nginx proxies /api)
		if (host !== 'localhost' && host !== '127.0.0.1') {
			return `${protocol}//${host}`;
		}
	}

	return 'http://localhost:8080';
}

export const API_BASE = getApiUrl();

function getUploadUrl(): string {
	if (PUBLIC_UPLOAD_URL && PUBLIC_UPLOAD_URL !== '' && PUBLIC_UPLOAD_URL !== 'undefined') {
		return PUBLIC_UPLOAD_URL;
	}

	if (browser) {
		const host = window.location.hostname;
		if (host !== 'localhost' && host !== '127.0.0.1') {
			const parts = host.split('.').slice(-2).join('.');
			return `${window.location.protocol}//upload.${parts}`;
		}
	}

	return API_BASE;
}

export const UPLOAD_BASE = getUploadUrl();
