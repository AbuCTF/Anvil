<script lang="ts">
	import { dialog } from '$lib/stores/dialog';
	import { tick } from 'svelte';

	let promptValue = '';
	let panel: HTMLDivElement | null = null;
	let primaryBtn: HTMLButtonElement | null = null;
	let promptInput: HTMLInputElement | null = null;
	let wasOpen = false;

	$: state = $dialog;

	// seed the prompt field + move focus to the primary control each time it opens.
	$: if (state.open && !wasOpen) {
		wasOpen = true;
		promptValue = state.defaultValue ?? '';
		tick().then(() => (state.kind === 'prompt' ? promptInput : primaryBtn)?.focus());
	} else if (!state.open && wasOpen) {
		wasOpen = false;
	}

	function cancel() {
		dialog.cancel(state.kind);
	}
	function accept() {
		if (state.kind === 'prompt') dialog.submitPrompt(promptValue);
		else if (state.kind === 'alert') dialog.cancel('alert');
		else dialog.accept();
	}
	function onKeydown(e: KeyboardEvent) {
		if (!state.open) return;
		if (e.key === 'Escape') {
			e.preventDefault();
			cancel();
		} else if (e.key === 'Enter' && state.kind !== 'prompt') {
			e.preventDefault();
			accept();
		}
	}
	// keep focus inside the panel (minimal trap).
	function trap(e: KeyboardEvent) {
		if (e.key !== 'Tab' || !panel) return;
		const f = panel.querySelectorAll<HTMLElement>(
			'button, input, [href], [tabindex]:not([tabindex="-1"])'
		);
		if (!f.length) return;
		const first = f[0];
		const last = f[f.length - 1];
		if (e.shiftKey && document.activeElement === first) {
			e.preventDefault();
			last.focus();
		} else if (!e.shiftKey && document.activeElement === last) {
			e.preventDefault();
			first.focus();
		}
	}
</script>

<svelte:window on:keydown={onKeydown} />

{#if state.open}
	<div
		class="dialog-overlay fixed inset-0 z-[200] flex items-center justify-center p-4"
		on:click|self={cancel}
		on:keydown={trap}
		role="presentation"
	>
		<div
			bind:this={panel}
			class="dialog-panel w-full max-w-sm rounded-lg border border-stone-800 bg-stone-950 p-5 shadow-2xl"
			role={state.kind === 'alert' ? 'alertdialog' : 'dialog'}
			aria-modal="true"
			aria-labelledby={state.title ? 'dialog-title' : undefined}
			aria-describedby="dialog-message"
		>
			{#if state.title}
				<h2 id="dialog-title" class="text-base font-semibold tracking-tight text-stone-100">
					{state.title}
				</h2>
			{/if}
			<p
				id="dialog-message"
				class="text-sm leading-relaxed text-stone-400 {state.title ? 'mt-1.5' : ''}"
			>
				{state.message}
			</p>

			{#if state.kind === 'prompt'}
				<input
					bind:this={promptInput}
					bind:value={promptValue}
					on:keydown={(e) => e.key === 'Enter' && (e.preventDefault(), accept())}
					placeholder={state.placeholder ?? ''}
					class="mt-3 w-full rounded-md border border-stone-800 bg-stone-900/60 px-3 py-2 text-sm text-stone-100 placeholder-stone-600 focus:border-stone-600 focus:outline-none focus:ring-1 focus:ring-stone-600"
				/>
			{/if}

			<div class="mt-5 flex justify-end gap-2.5">
				{#if state.kind !== 'alert'}
					<button
						on:click={cancel}
						class="rounded-md border border-stone-800 px-3.5 py-2 text-sm leading-none text-stone-300 transition-colors hover:border-stone-700 hover:bg-stone-800/40 hover:text-stone-100"
					>
						{state.cancelLabel ?? 'Cancel'}
					</button>
				{/if}
				<button
					bind:this={primaryBtn}
					on:click={accept}
					class="rounded-md px-3.5 py-2 text-sm font-medium leading-none transition-colors {state.danger
						? 'border border-down/30 bg-down/10 text-down hover:bg-down/20'
						: 'bg-stone-100 text-stone-950 hover:bg-white'}"
				>
					{state.confirmLabel ?? (state.kind === 'alert' ? 'OK' : 'Confirm')}
				</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.dialog-overlay {
		background: rgb(0 0 0 / 0.7);
		backdrop-filter: blur(4px);
		animation: dialog-fade 0.12s ease-out;
	}
	.dialog-panel {
		animation: dialog-pop 0.14s cubic-bezier(0.16, 1, 0.3, 1);
	}
	@keyframes dialog-fade {
		from {
			opacity: 0;
		}
	}
	@keyframes dialog-pop {
		from {
			opacity: 0;
			transform: translateY(6px) scale(0.98);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.dialog-overlay,
		.dialog-panel {
			animation: none;
		}
	}
</style>
