<script lang="ts">
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { platformInfo, refreshPlatformInfo } from '$lib/stores/platform';
	import BrandLogo from '$lib/components/BrandLogo.svelte';
	import InviteCodes from '$lib/components/admin/InviteCodes.svelte';

	export let settings: Record<string, any>;
	export let challenges: any[] = [];
	export let saving = false;
	export let changed = false;
	export let error = '';
	export let update: (key: string, value: any) => void;
	export let save: () => Promise<void>;

	let logoBusy = false;
	let logoError = '';

	const field = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 outline-none transition-colors focus:border-stone-500';
	const label = 'mb-1.5 block text-[11px] font-medium uppercase tracking-wider text-stone-500';

	$: published = challenges.filter((challenge) => challenge.status === 'published').length;
	$: eventName = typeof settings.platform_name === 'string' ? settings.platform_name.trim() : '';
	$: slug = typeof settings['event.slug'] === 'string' ? settings['event.slug'].trim() : '';
	$: timezone = typeof settings['event.timezone'] === 'string' ? settings['event.timezone'].trim() : '';
	$: start = typeof settings['event.start_at'] === 'string' ? settings['event.start_at'] : '';
	$: end = typeof settings['event.end_at'] === 'string' ? settings['event.end_at'] : '';
	$: scheduleReady = !!start && !!end && Number.isFinite(Date.parse(start)) && Number.isFinite(Date.parse(end)) && Date.parse(end) > Date.parse(start);
	$: pulseReady = !settings.market_pulse_enabled || settings.economy_mode;
	$: accentColor = ({ cyan: '#22d3ee', amber: '#f59e0b', emerald: '#34d399', violet: '#a78bfa' } as Record<string, string>)[settings['branding.accent'] ?? 'cyan'];
	$: requiredReady = !!eventName && /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/.test(slug) && !!timezone && scheduleReady && pulseReady;
	$: checks = [
		{ label: 'Identity', ready: !!eventName && !!slug && !!timezone },
		{ label: 'Schedule', ready: scheduleReady },
		{ label: settings.teams_mode ? 'Teams' : 'Solo', ready: ['open', 'invite', 'disabled'].includes(settings.registration_mode) },
		{ label: 'Features', ready: pulseReady },
		{ label: `${published} published`, ready: published > 0, warning: true }
	];

	function localValue(value: unknown) {
		if (typeof value !== 'string' || !value) return '';
		const date = new Date(value);
		if (!Number.isFinite(date.getTime())) return '';
		return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
	}

	function setTime(key: 'event.start_at' | 'event.end_at', value: string) {
		update(key, value ? new Date(value).toISOString() : '');
	}

	function suggestSlug() {
		const value = eventName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 64);
		if (value) update('event.slug', value);
	}

	async function uploadLogo(event: Event) {
		const input = event.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		logoBusy = true;
		logoError = '';
		try {
			await api.uploadBrandLogo(file);
			await refreshPlatformInfo();
		} catch (e) {
			logoError = e instanceof Error ? e.message : 'Logo upload failed';
		} finally {
			logoBusy = false;
			input.value = '';
		}
	}

	async function removeLogo() {
		logoBusy = true;
		logoError = '';
		try {
			await api.deleteBrandLogo();
			await refreshPlatformInfo();
		} catch (e) {
			logoError = e instanceof Error ? e.message : 'Logo removal failed';
		} finally {
			logoBusy = false;
		}
	}

	async function completeSetup() {
		if (!requiredReady) return;
		update('event.profile_managed', true);
		update('event.setup_completed', true);
		await save();
		await refreshPlatformInfo();
	}

	async function saveDraft() {
		update('event.profile_managed', true);
		await save();
	}
</script>

<div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
	<div class="space-y-6">
		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4">
				<h2 class="text-sm font-semibold text-stone-100">Identity and branding</h2>
			</div>
			<div class="grid gap-5 p-5 lg:grid-cols-[180px_minmax(0,1fr)]">
				<div class="rounded-lg border border-stone-800 bg-stone-950/70 p-4">
					<div class="flex h-24 items-center justify-center rounded-md border border-dashed border-stone-800 bg-stone-950 px-3">
						<BrandLogo className="max-h-16 max-w-full w-auto" />
					</div>
					<label class="mt-3 flex cursor-pointer items-center justify-center gap-2 rounded-md border border-stone-700 px-3 py-2 text-xs text-stone-300 hover:bg-stone-900">
						<Icon icon={logoBusy ? 'mdi:loading' : 'mdi:upload'} class="h-4 w-4 {logoBusy ? 'animate-spin' : ''}" />
						{settings['branding.logo_key'] ? 'Replace logo' : 'Upload logo'}
						<input type="file" accept="image/png,image/jpeg" class="sr-only" disabled={logoBusy} on:change={uploadLogo} />
					</label>
					{#if $platformInfo?.logo_url}
						<button type="button" class="mt-2 w-full text-xs text-stone-500 hover:text-down" disabled={logoBusy} on:click={removeLogo}>Remove</button>
					{/if}
					<p class="mt-2 text-center text-[10px] text-stone-600">PNG · JPEG · 2 MB</p>
					{#if logoError}<p class="mt-2 text-xs text-down">{logoError}</p>{/if}
				</div>
				<div class="grid gap-4 sm:grid-cols-2">
					<label class="sm:col-span-2"><span class={label}>Event name</span><input class={field} maxlength="100" value={settings.platform_name ?? ''} on:input={(e) => update('platform_name', (e.target as HTMLInputElement).value)} /></label>
					<label class="sm:col-span-2"><span class={label}>Tagline</span><textarea class="min-h-20 {field}" maxlength="280" value={settings.platform_description ?? ''} on:input={(e) => update('platform_description', (e.target as HTMLTextAreaElement).value)}></textarea></label>
					<label>
						<span class={label}>Event slug</span>
						<div class="flex gap-2"><input class={field} maxlength="64" value={settings['event.slug'] ?? ''} on:input={(e) => update('event.slug', (e.target as HTMLInputElement).value)} /><button type="button" class="rounded-md border border-stone-800 px-3 text-xs text-stone-400 hover:text-stone-200" on:click={suggestSlug}>Suggest</button></div>
					</label>
					<label><span class={label}>Organizer timezone</span><input class={field} list="event-timezones" value={settings['event.timezone'] ?? 'UTC'} on:input={(e) => update('event.timezone', (e.target as HTMLInputElement).value)} /></label>
					<datalist id="event-timezones"><option value="UTC"></option><option value="Asia/Kolkata"></option><option value="Asia/Singapore"></option><option value="Europe/London"></option><option value="America/New_York"></option><option value="America/Los_Angeles"></option></datalist>
					<label class="sm:col-span-2"><span class={label}>Contact email</span><input type="email" class={field} maxlength="254" placeholder="ctf@example.com" value={settings['event.contact_email'] ?? ''} on:input={(e) => update('event.contact_email', (e.target as HTMLInputElement).value)} /></label>
					<label class="sm:col-span-2"><span class={label}>Public event URL</span><input type="url" class={field} maxlength="1000" placeholder="https://ctf.example.com" value={settings['event.public_url'] ?? ''} on:input={(e) => update('event.public_url', (e.target as HTMLInputElement).value)} /></label>
					<label class="sm:col-span-2"><span class={label}>Accent palette</span><select class={field} value={settings['branding.accent'] ?? 'cyan'} on:change={(e) => update('branding.accent', (e.target as HTMLSelectElement).value)}><option value="cyan">Cyan</option><option value="amber">Amber</option><option value="emerald">Emerald</option><option value="violet">Violet</option></select></label>
				</div>
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Format and access</h2></div>
			<div class="grid gap-4 p-5 md:grid-cols-2">
				<label><span class={label}>Competition format</span><select class={field} value={String(settings.teams_mode ?? false)} on:change={(e) => update('teams_mode', (e.target as HTMLSelectElement).value === 'true')}><option value="false">Solo competition</option><option value="true">Team competition</option></select></label>
				<label><span class={label}>Self-registration</span><select class={field} value={settings.registration_mode ?? 'open'} on:change={(e) => update('registration_mode', (e.target as HTMLSelectElement).value)}><option value="open">Open registration</option><option value="invite">Invite only</option><option value="disabled">Closed</option></select></label>
				<label class="md:col-span-2"><span class={label}>Flag format</span><input class="font-mono {field}" maxlength="100" placeholder="flag&#123;...&#125;" value={settings['event.flag_format'] ?? 'flag{...}'} on:input={(e) => update('event.flag_format', (e.target as HTMLInputElement).value)} /></label>
				<label><span class={label}>Participant team creation</span><select class={field} value={settings['participants.team_creation'] ?? 'open'} on:change={(e) => update('participants.team_creation', (e.target as HTMLSelectElement).value)}><option value="open">Participants may create</option><option value="admin">Organizer managed</option><option value="disabled">Disabled</option></select></label>
				<label><span class={label}>Join by team code</span><select class={field} value={settings['participants.team_join'] ?? 'code'} on:change={(e) => update('participants.team_join', (e.target as HTMLSelectElement).value)}><option value="code">Enabled</option><option value="disabled">Organizer managed</option></select></label>
				<label><span class={label}>Default team size</span><input type="number" min="1" max="100" class={field} value={settings['participants.default_team_size'] ?? 4} on:input={(e) => update('participants.default_team_size', Number((e.target as HTMLInputElement).value))} /></label>
				<label><span class={label}>Maximum teams <span class="normal-case tracking-normal text-stone-700">(0 = unlimited)</span></span><input type="number" min="0" max="100000" class={field} value={settings['participants.max_teams'] ?? 0} on:input={(e) => update('participants.max_teams', Number((e.target as HTMLInputElement).value))} /></label>
				<label class="md:col-span-2"><span class={label}>Allowed email domains <span class="normal-case tracking-normal text-stone-700">(empty = any)</span></span><input class={field} maxlength="2000" placeholder="example.com, *.subsidiary.example" value={settings['participants.allowed_email_domains'] ?? ''} on:input={(e) => update('participants.allowed_email_domains', (e.target as HTMLInputElement).value)} /></label>
				<div class="flex flex-wrap gap-2 rounded-md border border-stone-800 p-3 text-[11px] md:col-span-2">
					<span class="rounded-full bg-emerald-500/10 px-2 py-1 text-emerald-400">Credentials</span>
					<span class="rounded-full px-2 py-1 {$platformInfo?.sso_enabled ? 'bg-emerald-500/10 text-emerald-400' : 'bg-stone-800 text-stone-500'}">SSO {$platformInfo?.sso_enabled ? 'on' : 'off'}</span>
					<span class="rounded-full px-2 py-1 {$platformInfo?.discord_walkin ? 'bg-emerald-500/10 text-emerald-400' : 'bg-stone-800 text-stone-500'}">Discord {$platformInfo?.discord_walkin ? 'on' : 'off'}</span>
				</div>
				{#if settings.registration_mode === 'invite'}<InviteCodes />{/if}
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Schedule</h2></div>
			<div class="grid gap-4 p-5 sm:grid-cols-2">
				<label><span class={label}>Starts</span><input type="datetime-local" step="60" class={field} value={localValue(start)} on:input={(e) => setTime('event.start_at', (e.target as HTMLInputElement).value)} /></label>
				<label><span class={label}>Ends</span><input type="datetime-local" step="60" class={field} value={localValue(end)} on:input={(e) => setTime('event.end_at', (e.target as HTMLInputElement).value)} /></label>
				{#if (start || end) && !scheduleReady}<p class="text-xs text-down sm:col-span-2">Set a valid end time after the start time.</p>{/if}
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Competition surface</h2></div>
			<div class="grid gap-3 p-5 sm:grid-cols-2">
				{#each [
					{ key: 'scoreboard_enabled', title: 'Scoreboard' },
					{ key: 'economy_mode', title: 'Ledger economy' },
					{ key: 'market_pulse_enabled', title: 'Market Pulse' },
					{ key: 'arena_enabled', title: 'Arena' },
					{ key: 'notifications.sound_allowed', title: 'Notification sounds' }
				] as feature}
					<label class="flex cursor-pointer items-center justify-between gap-4 rounded-md border border-stone-800 p-4 hover:border-stone-700">
						<span class="text-sm font-medium text-stone-200">{feature.title}</span>
						<input type="checkbox" class="h-4 w-4 accent-amber-500" checked={settings[feature.key] ?? false} on:change={(e) => update(feature.key, (e.target as HTMLInputElement).checked)} />
					</label>
				{/each}
				{#if !pulseReady}<p class="text-xs text-down sm:col-span-2">Market Pulse requires the Ledger economy.</p>{/if}
			</div>
		</section>

		<section class="rounded-lg border border-stone-800 bg-stone-900/25">
			<div class="border-b border-stone-800 px-5 py-4"><h2 class="text-sm font-semibold text-stone-100">Public links</h2></div>
			<div class="grid gap-4 p-5 sm:grid-cols-3">
				{#each [{ key: 'event.rules_url', label: 'Rules' }, { key: 'event.privacy_url', label: 'Privacy' }, { key: 'event.terms_url', label: 'Terms' }] as item}
					<label><span class={label}>{item.label} URL</span><input type="url" class={field} placeholder="https://…" value={settings[item.key] ?? ''} on:input={(e) => update(item.key, (e.target as HTMLInputElement).value)} /></label>
				{/each}
			</div>
		</section>
	</div>

	<aside class="xl:sticky xl:top-24 xl:self-start">
		<div class="rounded-lg border border-stone-800 bg-stone-950 p-4">
			<div class="flex items-center justify-between border-b border-stone-800 pb-3"><BrandLogo className="h-7 w-auto max-w-32" /><span class="text-[9px] uppercase tracking-widest text-stone-600">Participant preview</span></div>
			<div class="py-8 text-center">
				<p class="break-words text-2xl font-semibold text-stone-100">{eventName || 'Your event'}</p>
				<p class="mx-auto mt-3 max-w-xs text-xs leading-relaxed text-stone-500">{settings.platform_description || 'Your event description appears here.'}</p>
				<span class="mt-5 inline-flex rounded-md px-3 py-2 text-xs font-medium text-stone-950" style:background-color={accentColor}>View challenges</span>
			</div>
			<div class="flex justify-center gap-4 border-t border-stone-800 pt-3 text-[10px] text-stone-600"><span style:color={accentColor}>Challenges</span><span>Scoreboard</span><span>Team</span></div>
		</div>
		<div class="mt-4 rounded-lg border border-stone-800 bg-stone-900/40 p-5">
			<div class="flex items-center justify-between gap-3"><h2 class="text-sm font-semibold text-stone-100">Launch readiness</h2><span class="rounded-full px-2 py-1 text-[10px] {requiredReady ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400'}">{requiredReady ? 'Ready' : 'Needs attention'}</span></div>
			<div class="mt-4 divide-y divide-stone-800/70">
				{#each checks as check}
					<div class="flex items-center gap-3 py-3"><Icon icon={check.ready ? 'mdi:check-circle' : check.warning ? 'mdi:alert-circle-outline' : 'mdi:circle-outline'} class="h-4 w-4 shrink-0 {check.ready ? 'text-emerald-500' : check.warning ? 'text-amber-500' : 'text-stone-600'}" /><span class="text-xs font-medium text-stone-300">{check.label}</span></div>
				{/each}
			</div>
			{#if error}<p class="mt-3 text-xs text-down">{error}</p>{/if}
			<button type="button" class="mt-4 w-full rounded-md border border-stone-700 px-4 py-2.5 text-sm font-medium text-stone-200 disabled:cursor-not-allowed disabled:opacity-40" disabled={!changed || saving} on:click={saveDraft}>{saving ? 'Saving…' : 'Save draft'}</button>
			{#if !settings['event.setup_completed']}
				<button type="button" class="mt-2 w-full rounded-md bg-amber-500 px-4 py-2.5 text-sm font-medium text-stone-950 disabled:cursor-not-allowed disabled:opacity-40" disabled={!requiredReady || saving} on:click={completeSetup}>Complete setup</button>
			{/if}
			{#if changed}<p class="mt-2 text-center text-[11px] text-amber-500">Unsaved configuration changes</p>{/if}
		</div>
	</aside>
</div>
