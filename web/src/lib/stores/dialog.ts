import { writable } from 'svelte/store';

// promise-based replacements for native confirm()/alert()/prompt() so the whole
// app shares one styled modal instead of the browser's chrome dialogs.

export type DialogKind = 'confirm' | 'alert' | 'prompt';

export interface DialogRequest {
	kind: DialogKind;
	title?: string;
	message: string;
	confirmLabel?: string;
	cancelLabel?: string;
	danger?: boolean;
	// prompt only
	defaultValue?: string;
	placeholder?: string;
}

interface DialogState extends DialogRequest {
	open: boolean;
	resolve: ((value: unknown) => void) | null;
}

const initial: DialogState = { open: false, kind: 'confirm', message: '', resolve: null };

const store = writable<DialogState>(initial);

function open<T>(req: DialogRequest): Promise<T> {
	return new Promise<T>((resolve) => {
		store.set({ ...initial, ...req, open: true, resolve: resolve as (v: unknown) => void });
	});
}

// resolve the pending dialog and close it. value type depends on kind.
function settle(value: unknown) {
	store.update((s) => {
		s.resolve?.(value);
		return { ...initial };
	});
}

/** styled confirm(): resolves true if confirmed, false if cancelled/dismissed. */
export function confirmDialog(opts: Omit<DialogRequest, 'kind'>): Promise<boolean> {
	return open<boolean>({ ...opts, kind: 'confirm' });
}

/** styled alert(): resolves when acknowledged. */
export function alertDialog(opts: Omit<DialogRequest, 'kind' | 'cancelLabel'>): Promise<void> {
	return open<void>({ ...opts, kind: 'alert' });
}

/** styled prompt(): resolves the entered string, or null if cancelled. */
export function promptDialog(opts: Omit<DialogRequest, 'kind'>): Promise<string | null> {
	return open<string | null>({ ...opts, kind: 'prompt' });
}

export const dialog = {
	subscribe: store.subscribe,
	// confirm/alert
	accept: () => settle(true),
	dismiss: () => settle(false),
	// alert ack resolves void; confirm cancel resolves false; prompt cancel resolves null
	cancel: (kind: DialogKind) => settle(kind === 'prompt' ? null : kind === 'alert' ? undefined : false),
	submitPrompt: (value: string) => settle(value)
};
