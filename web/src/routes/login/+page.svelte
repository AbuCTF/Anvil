<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Icon from '@iconify/svelte';
	import { api } from '$api';
	import { auth } from '$stores/auth';

	let username = '';
	let password = '';
	let loading = false;
	let error = '';

	let discordWalkin = false;
	let emailWalkin = false;
	let discordLoading = false;
	let walkinName = '';
	let walkinEmail = '';
	let walkinLoading = false;
	let walkinMsg = '';

	// email fallback registers browser-direct against zeropool so the user stays
	// on this origin and turnstile + per-ip rate limits apply to the real client
	let zpBase = '';
	let eventSlug = '';
	let turnstileSiteKey = '';
	let turnstileToken = '';
	let turnstileEl: HTMLDivElement;

	onMount(async () => {
		try {
			const info = await api.getPlatformInfo();
			discordWalkin = info.discord_walkin;
			emailWalkin = info.email_walkin;
			zpBase = info.zeropool_base_url ?? '';
			eventSlug = info.zeropool_event_slug ?? '';
			turnstileSiteKey = info.turnstile_site_key ?? '';
			if (emailWalkin && turnstileSiteKey) {
				await tick();
				loadTurnstile();
			}
		} catch {
			// walk-in options stay hidden if platform info is unavailable
		}
	});

	function loadTurnstile() {
		const render = () => {
			// @ts-expect-error turnstile is injected by the cloudflare script
			if (window.turnstile && turnstileEl) {
				// @ts-expect-error injected global
				window.turnstile.render(turnstileEl, {
					sitekey: turnstileSiteKey,
					callback: (t: string) => (turnstileToken = t),
					'error-callback': () => (turnstileToken = '')
				});
			}
		};
		// @ts-expect-error injected global
		if (window.turnstile) return render();
		if (document.getElementById('cf-turnstile-script')) return;
		const s = document.createElement('script');
		s.id = 'cf-turnstile-script';
		s.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
		s.async = true;
		s.onload = render;
		document.head.appendChild(s);
	}

	async function discordSignIn() {
		discordLoading = true;
		error = '';
		try {
			const { authorize_url } = await api.discordAuthorizeUrl();
			window.location.href = authorize_url;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Discord sign-in is unavailable';
			discordLoading = false;
		}
	}

	async function emailSignIn() {
		if (!walkinName || !walkinEmail) {
			error = 'Enter your name and email';
			return;
		}
		if (turnstileSiteKey && !turnstileToken) {
			error = 'Please complete the verification';
			return;
		}
		walkinLoading = true;
		walkinMsg = '';
		error = '';
		try {
			const ev = await fetch(`${zpBase}/api/events/${eventSlug}`);
			if (!ev.ok) throw new Error('Registration is unavailable right now');
			const eventId = (await ev.json()).id;
			const body: Record<string, string> = { name: walkinName, email: walkinEmail };
			if (turnstileToken) body.turnstile_token = turnstileToken;
			const reg = await fetch(`${zpBase}/api/events/${eventId}/register`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			});
			const data = await reg.json().catch(() => ({}));
			if (!reg.ok) throw new Error(data.detail || data.message || 'Registration failed');
			walkinMsg = data.message || 'Check your email for a verification link, then sign in.';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Registration failed';
			turnstileToken = '';
			// @ts-expect-error injected global
			if (window.turnstile) try { window.turnstile.reset(); } catch {}
		} finally {
			walkinLoading = false;
		}
	}

	async function handleSubmit() {
		if (!username || !password) {
			error = 'Please fill in all fields';
			return;
		}

		loading = true;
		error = '';

		try {
			const response = await api.login(username, password);

			localStorage.setItem('accessToken', response.access_token);
			if (response.refresh_token) {
				localStorage.setItem('refreshToken', response.refresh_token);
			}

			auth.login(response.access_token, response.user, response.refresh_token);

			if (response.user.role === 'admin') {
				window.location.href = '/admin';
			} else {
				window.location.href = '/challenges';
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Login failed';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Login - Anvil</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] flex items-center justify-center px-4 py-12">
	<div class="w-full max-w-sm">
		<div class="text-center mb-6">
			<img src="/logo.png" alt="Anvil" class="h-9 w-auto mx-auto mb-4" />
			<h1 class="text-2xl font-semibold text-stone-100 tracking-tight">Welcome Back</h1>
			<p class="text-sm text-stone-500 mt-1.5">
				Don't have an account?
				<a href="/register" class="text-stone-200 font-medium hover:text-stone-50 transition-colors">Create one</a>
			</p>
		</div>

		<div class="bg-stone-900/40 border border-stone-800 rounded-lg p-5 sm:p-6">
			<form on:submit|preventDefault={handleSubmit} class="space-y-4">
				{#if error}
					<p class="flex items-start gap-1.5 text-danger text-sm">
						<Icon icon="mdi:alert-circle-outline" class="w-4 h-4 shrink-0 mt-0.5" />
						<span>{error}</span>
					</p>
				{/if}

				<div>
					<label for="username" class="block text-sm font-medium text-stone-300 mb-1.5">Username</label>
					<input
						id="username"
						type="text"
						autocomplete="username"
						bind:value={username}
						placeholder="Enter your username"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
				</div>

				<div>
					<label for="password" class="block text-sm font-medium text-stone-300 mb-1.5">Password</label>
					<input
						id="password"
						type="password"
						autocomplete="current-password"
						bind:value={password}
						placeholder="Enter your password"
						class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
					/>
				</div>

				<button
					type="submit"
					disabled={loading}
					class="w-full rounded-md bg-stone-100 text-stone-950 font-medium py-2.5 text-sm hover:bg-stone-50 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
				>
					{#if loading}
						<span class="flex items-center justify-center gap-2 leading-none">
							<Icon icon="mdi:loading" class="w-3.5 h-3.5 shrink-0 animate-spin" />
							Signing in…
						</span>
					{:else}
						Sign In
					{/if}
				</button>
			</form>

			{#if discordWalkin || emailWalkin}
				<div class="flex items-center gap-3 my-5">
					<div class="h-px flex-1 bg-stone-800"></div>
					<span class="text-xs uppercase tracking-wide text-stone-600">at the event</span>
					<div class="h-px flex-1 bg-stone-800"></div>
				</div>

				{#if discordWalkin}
					<button
						type="button"
						on:click={discordSignIn}
						disabled={discordLoading}
						class="w-full flex items-center justify-center gap-2 rounded-md bg-[#5865F2] text-white font-medium py-2.5 text-sm hover:bg-[#4752c4] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
					>
						<Icon icon={discordLoading ? 'mdi:loading' : 'ic:baseline-discord'} class="w-4 h-4 shrink-0 {discordLoading ? 'animate-spin' : ''}" />
						Continue with Discord
					</button>
				{/if}

				{#if emailWalkin}
					{#if walkinMsg}
						<p class="flex items-start gap-1.5 text-stone-400 text-sm mt-3">
							<Icon icon="mdi:email-check-outline" class="w-4 h-4 shrink-0 mt-0.5" />
							<span>{walkinMsg}</span>
						</p>
					{:else}
						<form on:submit|preventDefault={emailSignIn} class="mt-3 space-y-2">
							<input
								type="text"
								autocomplete="name"
								bind:value={walkinName}
								placeholder="Your name"
								class="w-full bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
							/>
							<div class="flex gap-2">
								<input
									type="email"
									autocomplete="email"
									bind:value={walkinEmail}
									placeholder="you@email.com"
									class="flex-1 min-w-0 bg-stone-900/60 border border-stone-800 rounded-md px-3 py-2.5 text-sm text-stone-200 placeholder-stone-600 focus:outline-none focus:border-stone-600 transition-colors"
								/>
								<button
									type="submit"
									disabled={walkinLoading}
									class="shrink-0 rounded-md border border-stone-700 text-stone-200 font-medium px-3 py-2.5 text-sm hover:bg-stone-800/60 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
								>
									{walkinLoading ? 'Sending…' : 'Email link'}
								</button>
							</div>
							{#if turnstileSiteKey}
								<div bind:this={turnstileEl}></div>
							{/if}
						</form>
					{/if}
				{/if}
			{/if}
		</div>
	</div>
</div>
