import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import Icons from 'unplugin-icons/vite';
import compression from 'vite-plugin-compression';

export default defineConfig({
	plugins: [
		sveltekit(),
		Icons({
			compiler: 'svelte'
		}),
		compression({
			algorithm: 'gzip',
			ext: '.gz'
		}),
		compression({
			algorithm: 'brotliCompress',
			ext: '.br'
		})
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
				drop_debugger: true
			}
		},
		rollupOptions: {
			output: {
				manualChunks: {
					'svelte-vendor': ['svelte'],
					'bits-ui': ['bits-ui']
				}
			}
		},
		chunkSizeWarningLimit: 1000
	}
});
