<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api, type MailDelivery, type MailProvider, type MailTemplate } from '$api';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import HelpTip from '$lib/components/HelpTip.svelte';
	import { alertDialog, confirmDialog, promptDialog } from '$lib/stores/dialog';
	import { formatLocalDateTimeWithZone } from '$lib/time';

	let section: 'providers' | 'templates' | 'deliveries' = 'providers';
	let loading = true;
	let error = '';
	let providers: MailProvider[] = [];
	let templates: MailTemplate[] = [];
	let deliveries: MailDelivery[] = [];
	let allowedVariables: string[] = [];
	let providerModal = false;
	let templateModal = false;
	let editingProvider: MailProvider | null = null;
	let editingTemplate: MailTemplate | null = null;
	let saving = false;
	let testing = '';
	let preview: { subject: string; body_html: string; body_text: string } | null = null;
	let providerForm = blankProvider();
	let templateForm = blankTemplate();

	const field = 'w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-sm text-stone-200 placeholder-stone-600 outline-none transition-colors focus:border-stone-500';
	const label = 'metadata-label mb-1.5 block text-stone-500';
	const primary = 'inline-flex items-center justify-center gap-2 rounded-md bg-amber-500/90 px-3.5 py-2 text-sm font-medium leading-none text-stone-950 transition-colors hover:bg-amber-500 disabled:cursor-not-allowed disabled:opacity-50';
	const ghost = 'inline-flex items-center justify-center gap-2 rounded-md border border-stone-700 px-3 py-2 text-xs font-medium leading-none text-stone-300 transition-colors hover:bg-stone-800/40 disabled:cursor-not-allowed disabled:opacity-50';

	function blankProvider() {
		return {
			name: '', host: '', port: 587, username: '', password: '', security: 'starttls',
			from_name: '', from_address: '', reply_to: '', priority: 10,
			daily_limit: null as number | null, hourly_limit: null as number | null,
			minute_limit: null as number | null, is_active: true
		};
	}

	function blankTemplate() {
		return {
			slug: '', name: '', description: '', subject: '',
			body_html: '<p>Hello {{participant_name}},</p>\n<p>Your message goes here.</p>',
			body_text: 'Hello {{participant_name}},\n\nYour message goes here.', is_active: true
		};
	}

	async function load() {
		loading = true;
		error = '';
		try {
			const [providerResult, templateResult, deliveryResult] = await Promise.all([
				api.getMailProviders(), api.getMailTemplates(), api.getMailDeliveries()
			]);
			providers = providerResult.providers;
			templates = templateResult.templates;
			allowedVariables = templateResult.allowed_variables;
			deliveries = deliveryResult.deliveries;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load email delivery.';
		} finally {
			loading = false;
		}
	}

	function applyPreset(kind: 'zepto' | 'brevo') {
		providerForm = {
			...providerForm,
			name: kind === 'zepto' ? 'ZeptoMail' : 'Brevo',
			host: kind === 'zepto' ? 'smtp.zeptomail.in' : 'smtp-relay.brevo.com',
			port: 587,
			security: 'starttls',
			username: kind === 'zepto' ? 'emailapikey' : providerForm.username,
			priority: kind === 'zepto' ? 1 : 2
		};
	}

	function openProvider(provider?: MailProvider) {
		editingProvider = provider ?? null;
		providerForm = provider ? {
			name: provider.name, host: provider.host, port: provider.port, username: provider.username,
			password: '', security: provider.security, from_name: provider.from_name,
			from_address: provider.from_address, reply_to: provider.reply_to, priority: provider.priority,
			daily_limit: provider.daily_limit ?? null, hourly_limit: provider.hourly_limit ?? null,
			minute_limit: provider.minute_limit ?? null, is_active: provider.is_active
		} : blankProvider();
		providerModal = true;
	}

	async function saveProvider() {
		saving = true;
		try {
			if (editingProvider) await api.updateMailProvider(editingProvider.id, providerForm);
			else await api.createMailProvider(providerForm);
			providerModal = false;
			await load();
		} catch (e) {
			await alertDialog({ title: 'Email provider', message: e instanceof Error ? e.message : 'Could not save the provider.' });
		} finally {
			saving = false;
		}
	}

	async function removeProvider(provider: MailProvider) {
		if (!(await confirmDialog({ title: 'Remove provider', message: `Remove ${provider.name}? Delivery history remains available.`, confirmLabel: 'Remove', danger: true }))) return;
		try {
			await api.deleteMailProvider(provider.id);
			await load();
		} catch (e) {
			await alertDialog({ title: 'Email provider', message: e instanceof Error ? e.message : 'Could not remove the provider.' });
		}
	}

	async function testProvider(provider: MailProvider) {
		const recipient = await promptDialog({ title: `Test ${provider.name}`, message: 'Send one test message to this address.', placeholder: 'recipient@example.com', confirmLabel: 'Send test' });
		if (!recipient) return;
		testing = provider.id;
		try {
			await api.testMailProvider(provider.id, recipient.trim());
			await alertDialog({ title: 'Test delivered', message: `A test message was accepted for ${recipient.trim()}.` });
			await load();
		} catch (e) {
			await alertDialog({ title: 'Test failed', message: e instanceof Error ? e.message : 'The provider rejected the test.' });
			await load();
		} finally {
			testing = '';
		}
	}

	function openTemplate(template?: MailTemplate) {
		editingTemplate = template ?? null;
		templateForm = template ? {
			slug: template.slug, name: template.name, description: template.description,
			subject: template.subject, body_html: template.body_html,
			body_text: template.body_text, is_active: template.is_active
		} : blankTemplate();
		preview = null;
		templateModal = true;
	}

	async function saveTemplate() {
		saving = true;
		try {
			if (editingTemplate) await api.updateMailTemplate(editingTemplate.slug, templateForm);
			else await api.createMailTemplate(templateForm);
			templateModal = false;
			await load();
		} catch (e) {
			await alertDialog({ title: 'Email template', message: e instanceof Error ? e.message : 'Could not save the template.' });
		} finally {
			saving = false;
		}
	}

	async function previewTemplate() {
		const samples: Record<string, string> = {
			participant_name: 'Avery Rao', event_name: 'Cyber Challenge', username: 'avery.rao',
			activation_url: 'https://ctf.example/activate/sample', reset_url: 'https://ctf.example/reset/sample',
			login_url: 'https://ctf.example/login', expires_at: '1 October 2026, 18:00 IST',
			event_start: '2 October 2026, 09:00 IST', event_end: '4 October 2026, 09:00 IST',
			support_email: 'support@example.com', team_name: 'Northstar', update_title: 'Schedule update',
			update_body: 'The next challenge wave opens at 14:00.', rank: '4', score: '1,240'
		};
		try {
			preview = await api.previewMailTemplate({ subject: templateForm.subject, body_html: templateForm.body_html, body_text: templateForm.body_text, values: samples });
		} catch (e) {
			await alertDialog({ title: 'Template preview', message: e instanceof Error ? e.message : 'Could not render the template.' });
		}
	}

	function insertVariable(variable: string) {
		templateForm = { ...templateForm, body_html: `${templateForm.body_html}{{${variable}}}` };
		preview = null;
	}

	function templateToken(variable: string) {
		return `{{${variable}}}`;
	}

	function providerTone(provider: MailProvider) {
		if (!provider.is_active) return 'text-stone-500 bg-stone-800/50';
		if (!provider.is_healthy) return 'text-warn bg-warn/10';
		return 'text-up bg-up/10';
	}

	onMount(load);
</script>

<div class="mt-8 border-t border-stone-800 pt-8">
	<div class="mb-5 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
		<div>
			<div class="flex items-center gap-2"><h2 class="text-base font-semibold text-stone-200">Email delivery</h2><HelpTip text="Configure one or more SMTP providers. Lower priority values are tried first; queued delivery fails over to the next healthy provider." /></div>
			<p class="mt-1 text-xs text-stone-500">Provider credentials are encrypted and never returned after saving.</p>
		</div>
		<div class="flex rounded-md border border-stone-800 bg-stone-950 p-1">
			{#each [{ id: 'providers', label: 'Providers' }, { id: 'templates', label: 'Templates' }, { id: 'deliveries', label: 'Delivery log' }] as item}
				<button type="button" on:click={() => section = item.id as typeof section} class="rounded px-3 py-1.5 text-xs transition-colors {section === item.id ? 'bg-stone-800 text-stone-200' : 'text-stone-500 hover:text-stone-300'}">{item.label}</button>
			{/each}
		</div>
	</div>

	{#if error}
		<div class="mb-4 flex items-center justify-between rounded-md border border-down/20 bg-down/[0.05] px-3 py-2.5 text-sm text-down"><span>{error}</span><button type="button" on:click={load} class="underline">Retry</button></div>
	{:else if loading}
		<div class="flex items-center justify-center gap-2 py-12 text-sm text-stone-500"><Icon icon="mdi:loading" class="h-4 w-4 animate-spin" /> Loading email delivery…</div>
	{:else if section === 'providers'}
		<div class="space-y-4">
			<div class="flex justify-end"><button type="button" on:click={() => openProvider()} class={primary}><Icon icon="mdi:plus" class="h-4 w-4" /> Add provider</button></div>
			{#if providers.length === 0}
				<EmptyState icon="mdi:email-outline" text="No email provider configured." />
			{:else}
				<div class="grid gap-4 xl:grid-cols-2">
					{#each providers as provider}
						<Card bodyClass="p-4">
							<div class="flex items-start justify-between gap-4">
								<div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><p class="font-medium text-stone-200">{provider.name}</p><span class="rounded-full px-2 py-0.5 text-[10px] {providerTone(provider)}">{provider.is_active ? provider.is_healthy ? 'Healthy' : 'Needs test' : 'Disabled'}</span><span class="text-[10px] text-stone-600">priority {provider.priority}</span></div><p class="mt-1 break-all font-mono text-xs text-stone-500">{provider.host}:{provider.port} · {provider.security.toUpperCase()}</p><p class="mt-1 text-xs text-stone-600">{provider.from_name} &lt;{provider.from_address}&gt;</p></div>
								<div class="flex shrink-0 gap-2"><button type="button" class={ghost} on:click={() => testProvider(provider)} disabled={testing === provider.id}>{testing === provider.id ? 'Testing…' : 'Test'}</button><button type="button" class={ghost} on:click={() => openProvider(provider)}>Edit</button></div>
							</div>
							<div class="mt-4 grid grid-cols-3 gap-2 text-xs"><div class="rounded-md bg-stone-950/60 p-2.5"><p class="metadata-label text-stone-600">Today</p><p class="mt-1 tabular-nums text-stone-300">{provider.daily_used}{provider.daily_limit ? ` / ${provider.daily_limit}` : ''}</p></div><div class="rounded-md bg-stone-950/60 p-2.5"><p class="metadata-label text-stone-600">Hour</p><p class="mt-1 tabular-nums text-stone-300">{provider.hourly_used}{provider.hourly_limit ? ` / ${provider.hourly_limit}` : ''}</p></div><div class="rounded-md bg-stone-950/60 p-2.5"><p class="metadata-label text-stone-600">Minute</p><p class="mt-1 tabular-nums text-stone-300">{provider.minute_used}{provider.minute_limit ? ` / ${provider.minute_limit}` : ''}</p></div></div>
							{#if provider.last_error_code}<p class="mt-3 text-xs text-warn">Last test: {provider.last_error_code.replaceAll('_', ' ')}</p>{/if}
							<div class="mt-4 flex justify-end"><button type="button" on:click={() => removeProvider(provider)} class="text-xs text-down hover:underline">Remove provider</button></div>
						</Card>
					{/each}
				</div>
			{/if}
		</div>
	{:else if section === 'templates'}
		<div class="space-y-4">
			<div class="flex justify-end"><button type="button" on:click={() => openTemplate()} class={primary}><Icon icon="mdi:plus" class="h-4 w-4" /> New template</button></div>
			<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
				{#each templates as template}
					<button type="button" on:click={() => openTemplate(template)} class="rounded-lg border border-stone-800 bg-stone-900/25 p-4 text-left transition-colors hover:border-stone-700 hover:bg-stone-900/50">
						<div class="flex items-start justify-between gap-3"><p class="text-sm font-medium text-stone-200">{template.name}</p><span class="rounded-full px-2 py-0.5 text-[10px] {template.is_active ? 'bg-up/10 text-up' : 'bg-stone-800 text-stone-500'}">{template.is_active ? 'Active' : 'Off'}</span></div>
						<p class="mt-1 font-mono text-[11px] text-stone-600">{template.slug}</p><p class="mt-3 line-clamp-2 text-xs text-stone-500">{template.subject}</p><p class="mt-3 text-[10px] text-stone-600">{template.variables.length} variable{template.variables.length === 1 ? '' : 's'}</p>
					</button>
				{/each}
			</div>
		</div>
	{:else}
		<Card bodyClass="p-0">
			{#if deliveries.length === 0}
				<EmptyState icon="mdi:email-fast-outline" text="No delivery attempts yet." />
			{:else}
				<div class="overflow-x-auto"><table class="w-full min-w-[760px] text-left text-xs"><thead><tr class="metadata-label border-b border-stone-800 text-stone-500"><th class="px-4 py-3">Recipient</th><th class="px-4 py-3">Purpose</th><th class="px-4 py-3">Provider</th><th class="px-4 py-3">Status</th><th class="px-4 py-3">Attempts</th><th class="px-4 py-3 text-right">Time</th></tr></thead><tbody>{#each deliveries as delivery}<tr class="border-b border-stone-800/60 last:border-0"><td class="px-4 py-3 text-stone-300">{delivery.recipient}</td><td class="px-4 py-3 font-mono text-stone-500">{delivery.template_slug ?? 'provider_test'}</td><td class="px-4 py-3 text-stone-500">{delivery.provider_name ?? '—'}</td><td class="px-4 py-3"><span class="{delivery.status === 'sent' ? 'text-up' : delivery.status === 'failed' ? 'text-down' : 'text-warn'}">{delivery.status}</span>{#if delivery.error_code}<span class="ml-2 text-stone-600">{delivery.error_code.replaceAll('_', ' ')}</span>{/if}</td><td class="px-4 py-3 tabular-nums text-stone-500">{delivery.attempts}/{delivery.max_attempts}</td><td class="px-4 py-3 text-right text-stone-600">{formatLocalDateTimeWithZone(delivery.sent_at ?? delivery.created_at)}</td></tr>{/each}</tbody></table></div>
			{/if}
		</Card>
	{/if}
</div>

{#if providerModal}
	<div class="fixed inset-0 z-[70] flex items-center justify-center p-4"><button type="button" aria-label="Close provider dialog" class="absolute inset-0 bg-black/70" on:click={() => providerModal = false}></button><div class="relative max-h-[92vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-stone-800 bg-stone-950 shadow-2xl">
		<div class="flex items-start justify-between border-b border-stone-800 p-5"><div><h3 class="font-semibold text-stone-100">{editingProvider ? 'Edit provider' : 'Add provider'}</h3><p class="mt-1 text-xs text-stone-500">SMTP credentials are encrypted before storage.</p></div><button type="button" on:click={() => providerModal = false} class="text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button></div>
		<form on:submit|preventDefault={saveProvider} class="space-y-5 p-5">
			{#if !editingProvider}<div class="flex flex-wrap gap-2"><span class="py-2 text-xs text-stone-600">Quick start</span><button type="button" class={ghost} on:click={() => applyPreset('zepto')}>ZeptoMail</button><button type="button" class={ghost} on:click={() => applyPreset('brevo')}>Brevo SMTP</button></div>{/if}
			<div class="grid gap-4 sm:grid-cols-2"><label><span class={label}>Name</span><input bind:value={providerForm.name} required maxlength="100" class={field} placeholder="ZeptoMail primary" /></label><label><span class={label}>Priority <HelpTip text="Lower numbers are preferred. Use 1 for primary and 2 for fallback." /></span><input type="number" bind:value={providerForm.priority} min="0" max="1000" required class={field} /></label><label><span class={label}>SMTP host</span><input bind:value={providerForm.host} required class={field} placeholder="smtp.zeptomail.in" /></label><label><span class={label}>Port</span><input type="number" bind:value={providerForm.port} min="1" max="65535" required class={field} /></label><label><span class={label}>Security</span><select bind:value={providerForm.security} class={field}><option value="starttls">STARTTLS</option><option value="tls">Implicit TLS</option></select></label><label><span class={label}>Username</span><input bind:value={providerForm.username} class={field} autocomplete="off" /></label><label class="sm:col-span-2"><span class={label}>Password {editingProvider ? '(leave blank to keep current)' : ''}</span><input type="password" bind:value={providerForm.password} required={!editingProvider} class={field} autocomplete="new-password" /></label></div>
			<div class="grid gap-4 border-t border-stone-800 pt-5 sm:grid-cols-2"><label><span class={label}>Sender name</span><input bind:value={providerForm.from_name} required class={field} placeholder="Cyber Challenge" /></label><label><span class={label}>From address</span><input type="email" bind:value={providerForm.from_address} required class={field} /></label><label class="sm:col-span-2"><span class={label}>Reply-to address</span><input type="email" bind:value={providerForm.reply_to} class={field} /></label></div>
			<div class="grid grid-cols-3 gap-3 border-t border-stone-800 pt-5"><label><span class={label}>Daily cap</span><input type="number" bind:value={providerForm.daily_limit} min="1" class={field} placeholder="None" /></label><label><span class={label}>Hourly cap</span><input type="number" bind:value={providerForm.hourly_limit} min="1" class={field} placeholder="None" /></label><label><span class={label}>Minute cap</span><input type="number" bind:value={providerForm.minute_limit} min="1" class={field} placeholder="None" /></label></div>
			<label class="flex items-start gap-3 rounded-md border border-stone-800 bg-stone-900/30 p-3"><input type="checkbox" bind:checked={providerForm.is_active} class="mt-0.5" /><span><span class="block text-sm text-stone-300">Provider enabled</span><span class="mt-1 block text-xs text-stone-600">A new or rotated provider remains untrusted until a test succeeds.</span></span></label>
			<div class="flex justify-end gap-2"><button type="button" class={ghost} on:click={() => providerModal = false}>Cancel</button><button type="submit" class={primary} disabled={saving}>{saving ? 'Saving…' : 'Save provider'}</button></div>
		</form>
	</div></div>
{/if}

{#if templateModal}
	<div class="fixed inset-0 z-[70] flex items-center justify-center p-4"><button type="button" aria-label="Close template dialog" class="absolute inset-0 bg-black/70" on:click={() => templateModal = false}></button><div class="relative flex max-h-[94vh] w-full max-w-6xl flex-col overflow-hidden rounded-xl border border-stone-800 bg-stone-950 shadow-2xl">
		<div class="flex items-start justify-between border-b border-stone-800 p-5"><div><h3 class="font-semibold text-stone-100">{editingTemplate ? 'Edit template' : 'New template'}</h3><p class="mt-1 text-xs text-stone-500">Variables are allowlisted and participant values are escaped in HTML.</p></div><button type="button" on:click={() => templateModal = false} class="text-stone-500 hover:text-stone-200"><Icon icon="mdi:close" class="h-5 w-5" /></button></div>
		<form on:submit|preventDefault={saveTemplate} class="grid min-h-0 flex-1 overflow-y-auto lg:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]">
			<div class="space-y-4 border-stone-800 p-5 lg:border-r"><div class="grid gap-4 sm:grid-cols-2"><label><span class={label}>Template name</span><input bind:value={templateForm.name} required maxlength="160" class={field} /></label><label><span class={label}>Purpose slug</span><input bind:value={templateForm.slug} disabled={!!editingTemplate} required pattern={'[a-z][a-z0-9_]{2,63}'} class="font-mono {field}" placeholder="participant_invite" /></label></div><label class="block"><span class={label}>Description</span><input bind:value={templateForm.description} class={field} /></label><label class="block"><span class={label}>Subject</span><input bind:value={templateForm.subject} required maxlength="500" class={field} placeholder={'Welcome to {{event_name}}'} /></label><label class="block"><span class={label}>HTML body</span><textarea bind:value={templateForm.body_html} required rows="12" class="font-mono text-xs {field}"></textarea></label><label class="block"><span class={label}>Plain-text fallback</span><textarea bind:value={templateForm.body_text} rows="7" class="font-mono text-xs {field}"></textarea></label><label class="flex items-center gap-2 text-sm text-stone-400"><input type="checkbox" bind:checked={templateForm.is_active} /> Active</label><div class="flex justify-end gap-2"><button type="button" class={ghost} on:click={() => templateModal = false}>Cancel</button><button type="button" class={ghost} on:click={previewTemplate}>Preview</button><button type="submit" class={primary} disabled={saving}>{saving ? 'Saving…' : 'Save template'}</button></div></div>
			<div class="space-y-5 bg-stone-900/20 p-5"><div><p class="metadata-label text-stone-500">Available variables</p><div class="mt-2 flex flex-wrap gap-1.5">{#each allowedVariables as variable}<button type="button" on:click={() => insertVariable(variable)} class="rounded border border-stone-800 bg-stone-950 px-2 py-1 font-mono text-[10px] text-stone-500 hover:text-stone-300">{templateToken(variable)}</button>{/each}</div></div>{#if preview}<div><p class="metadata-label text-stone-500">Rendered subject</p><p class="mt-2 rounded-md border border-stone-800 bg-stone-950 p-3 text-sm text-stone-300">{preview.subject}</p></div><div><p class="metadata-label text-stone-500">Rendered email</p><iframe title="Email preview" sandbox="" srcdoc={preview.body_html} class="mt-2 h-80 w-full rounded-md border border-stone-800 bg-white"></iframe></div>{:else}<div class="grid h-80 place-items-center rounded-md border border-dashed border-stone-800 text-center"><div><Icon icon="mdi:email-edit-outline" class="mx-auto h-8 w-8 text-stone-700" /><p class="mt-2 text-xs text-stone-600">Preview uses safe sample participant and event values.</p></div></div>{/if}</div>
		</form>
	</div></div>
{/if}
