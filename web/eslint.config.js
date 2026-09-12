import js from '@eslint/js';
import globals from 'globals';
import svelte from 'eslint-plugin-svelte';
import ts from 'typescript-eslint';
import svelteConfig from './svelte.config.js';

export default [
	{
		ignores: ['.svelte-kit/**', 'build/**', 'node_modules/**']
	},
	js.configs.recommended,
	...ts.configs.recommended,
	...svelte.configs['flat/recommended'],
	{
		languageOptions: {
			globals: {
				...globals.browser,
				...globals.node
			}
		},
		rules: {
			'@typescript-eslint/no-explicit-any': 'off',
			'svelte/no-immutable-reactive-statements': 'off',
			'svelte/no-reactive-functions': 'off',
			'svelte/prefer-svelte-reactivity': 'off',
			'svelte/require-each-key': 'off'
		}
	},
	{
		files: ['**/*.svelte'],
		languageOptions: {
			parserOptions: {
				parser: ts.parser,
				svelteConfig
			}
		},
		rules: {
			'no-useless-assignment': 'off',
			'svelte/no-navigation-without-resolve': 'off'
		}
	}
];
