/** @type {import('tailwindcss').Config} */
export default {
	content: ['./src/**/*.{html,js,svelte,ts}'],
	darkMode: 'class',
	theme: {
		extend: {
			colors: {
				// Neutral scale driven by CSS variables so the whole UI flips between
				// dark and light themes (see app.css). Meaning stays constant: 950 = page
				// ground, 100 = primary text; the values invert per theme.
				stone: {
					50: 'rgb(var(--st-50) / <alpha-value>)',
					100: 'rgb(var(--st-100) / <alpha-value>)',
					200: 'rgb(var(--st-200) / <alpha-value>)',
					300: 'rgb(var(--st-300) / <alpha-value>)',
					400: 'rgb(var(--st-400) / <alpha-value>)',
					500: 'rgb(var(--st-500) / <alpha-value>)',
					600: 'rgb(var(--st-600) / <alpha-value>)',
					700: 'rgb(var(--st-700) / <alpha-value>)',
					800: 'rgb(var(--st-800) / <alpha-value>)',
					900: 'rgb(var(--st-900) / <alpha-value>)',
					950: 'rgb(var(--st-950) / <alpha-value>)'
				},
				// Minimal accent - muted gold
				accent: {
					DEFAULT: '#a8935c',
					light: '#c4b084',
					dark: '#8c7847'
				},
				// Semantic status — muted, reserved for state only (see DESIGN.md).
				up: '#6fae7f',
				down: '#d3776f',
				danger: '#f0524f',
				warn: '#cba64a',
				info: '#6f95bd',
				blood: '#e0483c'
			},
			fontFamily: {
				sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'Roboto', 'Helvetica', 'Arial', 'sans-serif'],
				mono: ['JetBrains Mono', 'SF Mono', 'Consolas', 'Liberation Mono', 'Menlo', 'Courier', 'monospace']
			}
		}
	},
	plugins: []
};
