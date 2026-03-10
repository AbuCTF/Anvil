import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit()
	],
	server: {
		port: 3000,
		host: true
	},
	build: {
		minify: 'terser',
		terserOptions: {
			compress: {
				drop_console: true,
				drop_debugger: true,
				passes: 2
			}
		},
		rollupOptions: {
			output: {
				manualChunks: {
					'svelte-vendor': ['svelte'],
					'bits-ui': ['bits-ui'],
					'icons': ['@iconify/svelte']
				}
			}
		},
		chunkSizeWarningLimit: 500,
		cssCodeSplit: true,
		reportCompressedSize: false
	}
});
