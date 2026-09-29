import { browser } from '$app/environment';

const preferenceKey = 'anvil.notificationSound';
const soundedKey = 'anvil.notificationSound.last';
let context: AudioContext | null = null;

export function notificationSoundEnabled() {
	return browser && localStorage.getItem(preferenceKey) === 'on';
}

export function setNotificationSound(enabled: boolean) {
	if (!browser) return;
	localStorage.setItem(preferenceKey, enabled ? 'on' : 'off');
	if (enabled) primeNotificationSound();
}

export function primeNotificationSound() {
	if (!browser || !notificationSoundEnabled()) return;
	context ??= new AudioContext();
	if (context.state === 'suspended') void context.resume();
}

export function playNotificationSound(id: string) {
	if (!browser || !notificationSoundEnabled() || !context || context.state !== 'running') return;
	if (localStorage.getItem(soundedKey) === id) return;
	const oscillator = context.createOscillator();
	const gain = context.createGain();
	oscillator.type = 'sine';
	oscillator.frequency.setValueAtTime(660, context.currentTime);
	oscillator.frequency.exponentialRampToValueAtTime(880, context.currentTime + 0.12);
	gain.gain.setValueAtTime(0.0001, context.currentTime);
	gain.gain.exponentialRampToValueAtTime(0.08, context.currentTime + 0.015);
	gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 0.22);
	oscillator.connect(gain).connect(context.destination);
	oscillator.start();
	oscillator.stop(context.currentTime + 0.24);
	localStorage.setItem(soundedKey, id);
}
