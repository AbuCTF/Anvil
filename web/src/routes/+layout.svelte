<script lang="ts">
	import '@fontsource-variable/jetbrains-mono/wght.css';
	import '@fontsource-variable/inter/wght.css';
	import '../app.css';
	import { onMount } from 'svelte';
	import { page, navigating } from '$app/stores';
	import { goto } from '$app/navigation';
	import { browser } from '$app/environment';
	import { auth } from '$stores/auth';
	import { api } from '$api';
	import Icon from '@iconify/svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import EventClock from '$lib/components/EventClock.svelte';
	import { loadPlatformInfo, registerHref, platformInfo } from '$lib/stores/platform';
	import RankBadge from '$lib/components/RankBadge.svelte';

	let mobileMenuOpen = false;
	let userMenuOpen = false;

	// nav is feature-flag-driven off /info: the admin panel decides the event's
	// shape (Arena, Teams, VPN, Scoreboard) and both the tabs and the routes follow.
	$: navigation = [
		{ name: 'Challenges', href: '/challenges', icon: 'mdi:flag' },
		...($platformInfo?.scoreboard_enabled !== false ? [{ name: 'Scoreboard', href: '/scoreboard', icon: 'mdi:trophy' }] : []),
		...($platformInfo?.arena_enabled ? [{ name: 'Arena', href: '/arena', icon: 'mdi:sword-cross' }] : []),
		{ name: 'Instances', href: '/instances', icon: 'mdi:server' }
	];

	// "My Instances" dropped here — the top-nav Instances tab already covers it.
	$: userMenu = [
		...($auth.user?.role === 'admin' ? [{ name: 'Admin', href: '/admin', icon: 'mdi:shield-crown' }] : []),
		{ name: 'Profile', href: '/profile', icon: 'mdi:account' },
		...($platformInfo?.teams_mode ? [{ name: 'Team', href: '/team', icon: 'mdi:account-group' }] : []),
		...($platformInfo?.vpn_enabled ? [{ name: 'VPN', href: '/vpn', icon: 'mdi:vpn' }] : [])
	];

	// route guard: a disabled feature's URL redirects home, so hidden != reachable.
	$: if (browser && $platformInfo) {
		const p = $page.url.pathname;
		const blocked =
			(p.startsWith('/arena') && !$platformInfo.arena_enabled) ||
			(p.startsWith('/team') && !$platformInfo.teams_mode) ||
			(p.startsWith('/vpn') && !$platformInfo.vpn_enabled) ||
			(p.startsWith('/scoreboard') && $platformInfo.scoreboard_enabled === false);
		if (blocked) goto('/challenges');
	}

	const iconMetrics: Record<string, { size: number }> = {
		'mdi:flag': { size: 15.5 },
		'mdi:trophy': { size: 13.5 },
		'mdi:account-group': { size: 15 },
		'mdi:sword-cross': { size: 14 },
		'mdi:server': { size: 12.75 },
		'mdi:shield-crown': { size: 12.75 },
		'mdi:chevron-down': { size: 14 },
		'mdi:logout': { size: 13.5 }
	};

	function iconMetric(icon: string) {
		return iconMetrics[icon] ?? { size: 14 };
	}

	let theme: 'dark' | 'light' = 'dark';
	let credits: number | null = null;

	async function loadCredits() {
		// only poll /economy/me when the economy is actually on — otherwise it 400s
		if (!$auth.isAuthenticated || !$platformInfo?.economy_enabled) {
			credits = null;
			return;
		}
		try {
			const e = await api.getEconomy();
			credits = e.credits;
		} catch {
			credits = null;
		}
	}
	$: if ($auth.isAuthenticated && $platformInfo?.economy_enabled && $page.url.pathname) loadCredits();

	onMount(() => {
		auth.checkAuth();
		loadPlatformInfo();
		theme = document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
		const refreshVisibleRank = () => {
			if (!document.hidden) void auth.refreshRank();
		};
		document.addEventListener('visibilitychange', refreshVisibleRank);
		window.addEventListener('focus', refreshVisibleRank);
		return () => {
			document.removeEventListener('visibilitychange', refreshVisibleRank);
			window.removeEventListener('focus', refreshVisibleRank);
		};
	});

	function toggleTheme() {
		theme = theme === 'dark' ? 'light' : 'dark';
		if (theme === 'light') document.documentElement.setAttribute('data-theme', 'light');
		else document.documentElement.removeAttribute('data-theme');
		try {
			localStorage.setItem('theme', theme);
		} catch {
			/* ignore */
		}
	}

	function handleLogout() {
		auth.logout();
		userMenuOpen = false;
	}
</script>

{#if $navigating}
	<div class="navbar-progress" aria-hidden="true"></div>
{/if}

<div class="min-h-screen bg-stone-950 text-stone-100 flex flex-col">
	<nav class="border-b border-stone-800 bg-stone-950/95 backdrop-blur-sm sticky top-0 z-50">
		<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10">
			<div class="grid h-16 grid-cols-[auto_1fr_auto] items-center lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
				<a href="/" class="col-start-1 row-start-1 flex shrink-0 items-center justify-self-start">
					<img src="/logo.png" alt="Anvil" class="h-10 w-auto" />
				</a>

				<div class="col-start-2 row-start-1 hidden items-center justify-self-center lg:flex">
					<div class="flex items-center bg-stone-900/50 rounded-full p-1 border border-stone-800/50">
						{#each navigation as item}
							<a
								href={item.href}
								aria-label={item.name}
								title={item.name}
								class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm leading-none font-medium rounded-full transition-all duration-200 xl:px-4
								{$page.url.pathname.startsWith(item.href)
									? 'bg-stone-800 text-stone-50'
									: 'text-stone-400 hover:text-stone-50'}"
							>
								<OpticalIcon icon={item.icon} {...iconMetric(item.icon)} box={16} />
								<span class="optical-label hidden leading-[14px] xl:inline">{item.name}</span>
							</a>
						{/each}
					</div>
				</div>

				<div class="col-start-3 row-start-1 flex min-w-0 shrink-0 items-center justify-self-end gap-1 lg:gap-2 xl:gap-3">
					{#if credits !== null}
						<span class="group relative hidden h-6 items-center gap-1.5 rounded-full border border-amber-500/25 bg-amber-500/[0.07] px-2.5 text-xs font-medium leading-none text-amber-500 tabular-nums sm:inline-flex">
							<OpticalIcon icon="mdi:diamond-stone" size={13} box={14} />
							<span class="optical-label">{Math.round(credits)}</span>
							<span class="pointer-events-none absolute right-0 top-full z-50 mt-2 hidden w-64 rounded-md border border-stone-800 bg-stone-950 p-3 text-left text-xs font-normal leading-relaxed text-stone-400 shadow-lg group-hover:block">
								<span class="mb-1 block font-medium text-stone-200">Credits</span>
								Your team's spendable budget. Opening a challenge costs credits, and a clean solve refunds half. Convert points to credits on the Team page, or claim a one-time bailout if you run out.
							</span>
						</span>
					{/if}
					<EventClock className="mr-1 shrink-0 lg:mr-0" />
					<div class="hidden items-center gap-0.5 lg:flex">
						<button
							on:click={toggleTheme}
							aria-label="Toggle theme"
							title="Toggle theme"
							class="rounded-md p-1.5 text-stone-400 transition-colors hover:bg-stone-800/40 hover:text-stone-100"
						>
							<Icon icon={theme === 'dark' ? 'mdi:weather-sunny' : 'mdi:weather-night'} class="h-5 w-5" />
						</button>
						{#if $auth.isAuthenticated}
							{#if $auth.user?.role === 'admin'}
								<a
									href="/admin"
									class="flex items-center gap-1.5 px-2 py-1.5 text-sm leading-none font-medium text-amber-500 transition-colors hover:text-amber-400 2xl:px-3"
								>
									<OpticalIcon icon="mdi:shield-crown" {...iconMetric('mdi:shield-crown')} box={16} />
									<span class="optical-label hidden leading-[14px] 2xl:inline">Admin</span>
								</a>
							{/if}
							<div class="relative">
							<button
								on:click={() => userMenuOpen = !userMenuOpen}
								aria-haspopup="menu"
								aria-expanded={userMenuOpen}
								class="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm leading-none text-stone-300 transition-colors hover:bg-stone-800/40 hover:text-stone-100 2xl:px-3"
							>
								<span class="hidden 2xl:inline-flex">
									<RankBadge rank={$auth.user?.rank ?? 0} />
								</span>
								<span class="optical-label max-w-[28vw] truncate whitespace-nowrap font-medium leading-[14px] 2xl:max-w-64">{$auth.user?.username}</span>
								<OpticalIcon
									icon="mdi:chevron-down"
									{...iconMetric('mdi:chevron-down')}
									box={14}
									className="text-stone-500"
								/>
							</button>

							{#if userMenuOpen}
								<div class="absolute right-0 mt-2 w-64 bg-stone-950 backdrop-blur-md border border-stone-800 rounded-lg z-50 overflow-hidden">
									<div class="px-4 py-3 border-b border-stone-800">
										<p class="truncate text-sm font-medium text-stone-100">{$auth.user?.username}</p>
										<p class="truncate text-xs text-stone-500" title={$auth.user?.email || undefined}>{$auth.user?.email || 'No email'}</p>
									</div>
									{#each userMenu as item}
										<a
											href={item.href}
											on:click={() => userMenuOpen = false}
											class="flex items-center gap-3 px-4 py-2.5 text-sm leading-none text-stone-400 hover:bg-stone-800/40 hover:text-stone-100 transition-colors"
										>
											<OpticalIcon icon={item.icon} {...iconMetric(item.icon)} box={16} />
											<span class="optical-label leading-[14px]">{item.name}</span>
										</a>
									{/each}
									<div class="border-t border-stone-800">
										<button
											on:click={handleLogout}
											class="flex items-center gap-3 px-4 py-2.5 text-sm leading-none text-stone-400 hover:bg-stone-800/40 hover:text-danger transition-colors w-full text-left"
										>
											<OpticalIcon icon="mdi:logout" {...iconMetric('mdi:logout')} box={16} />
											<span class="optical-label leading-[14px]">Logout</span>
										</button>
									</div>
								</div>
							{/if}
							</div>
						{:else}
							<a
								href="/login"
								class="px-3 py-2 text-sm font-medium text-stone-400 transition-colors hover:text-stone-100"
							>
								Login
							</a>
							<a
								href={$registerHref}
								class="inline-grid h-8 w-[86px] shrink-0 place-items-center rounded-full border border-amber-500/25 bg-amber-500/[0.07] text-xs font-medium leading-none text-amber-500 transition-colors hover:border-amber-500/40 hover:bg-amber-500/[0.12] hover:text-amber-400"
							>
								<span class="relative -top-[0.5px] leading-none">Register</span>
							</a>
						{/if}
					</div>
					<div class="flex items-center gap-1 lg:hidden">
						<button on:click={toggleTheme} aria-label="Toggle theme" class="p-2 text-stone-400 hover:text-stone-100">
							<Icon icon={theme === 'dark' ? 'mdi:weather-sunny' : 'mdi:weather-night'} class="w-5 h-5" />
						</button>
						<button
							on:click={() => (mobileMenuOpen = !mobileMenuOpen)}
							aria-label={mobileMenuOpen ? 'Close navigation' : 'Open navigation'}
							aria-expanded={mobileMenuOpen}
							class="p-2 text-stone-400 hover:text-stone-100"
						>
							<Icon icon={mobileMenuOpen ? 'mdi:close' : 'mdi:menu'} class="w-5 h-5" />
						</button>
					</div>
				</div>
			</div>
		</div>

		{#if mobileMenuOpen}
			<div class="border-t border-stone-800 bg-stone-950/95 backdrop-blur-md lg:hidden">
				<div class="px-4 py-3 space-y-1">
					{#each navigation as item}
						<a
							href={item.href}
							on:click={() => mobileMenuOpen = false}
							class="flex items-center gap-3 px-3 py-2.5 text-sm leading-none font-medium rounded-md transition-colors
							{$page.url.pathname.startsWith(item.href)
								? 'text-amber-500'
								: 'text-stone-400 hover:bg-stone-800/40 hover:text-stone-100'}"
						>
						<OpticalIcon icon={item.icon} {...iconMetric(item.icon)} box={16} />
						<span class="optical-label leading-[14px]">{item.name}</span>
						</a>
					{/each}

					{#if $auth.isAuthenticated}
						<div class="border-t border-stone-800 pt-3 mt-3">
							<div class="flex items-center justify-between gap-3 px-3 pb-2.5">
								<div class="min-w-0">
									<p class="truncate text-sm font-medium text-stone-200">{$auth.user?.username}</p>
									<p class="truncate text-xs text-stone-600">{$auth.user?.email || 'Signed in'}</p>
								</div>
								<RankBadge rank={$auth.user?.rank ?? 0} />
							</div>
							{#each userMenu as item}
								<a
									href={item.href}
									on:click={() => mobileMenuOpen = false}
									class="flex items-center gap-3 px-3 py-2.5 text-sm leading-none font-medium text-stone-400 hover:bg-stone-800/40 hover:text-stone-100 rounded-md transition-colors"
								>
									<OpticalIcon icon={item.icon} {...iconMetric(item.icon)} box={16} />
									<span class="optical-label leading-[14px]">{item.name}</span>
								</a>
							{/each}
							<button
								on:click={() => { handleLogout(); mobileMenuOpen = false; }}
								class="flex items-center gap-3 px-3 py-2.5 text-sm leading-none font-medium text-stone-400 hover:bg-stone-800/40 hover:text-danger rounded-md transition-colors w-full text-left"
							>
								<OpticalIcon icon="mdi:logout" {...iconMetric('mdi:logout')} box={16} />
								<span class="optical-label leading-[14px]">Logout</span>
							</button>
						</div>
					{:else}
						<div class="border-t border-stone-800 pt-3 mt-3 flex gap-3">
							<a href="/login" on:click={() => mobileMenuOpen = false} class="flex-1 px-4 py-2.5 text-center text-sm font-medium text-stone-300 border border-stone-800 rounded-md hover:bg-stone-800/40 hover:text-stone-100 transition-colors">Login</a>
							<a href={$registerHref} on:click={() => mobileMenuOpen = false} class="flex-1 px-4 py-2.5 text-center text-sm font-semibold text-amber-950 bg-amber-500 rounded-full hover:bg-amber-400 transition-colors">Register</a>
						</div>
					{/if}
				</div>
			</div>
		{/if}
	</nav>

	<main class="flex-1">
		<slot />
	</main>
</div>

<style>
	/* indeterminate top progress bar during route navigation */
	.navbar-progress {
		position: fixed;
		top: 0;
		left: 0;
		height: 2px;
		width: 100%;
		z-index: 200;
		background: linear-gradient(90deg, transparent, rgb(245 158 11 / 0.9), transparent);
		transform-origin: left;
		animation: navbar-progress 1.1s ease-in-out infinite;
	}
	@keyframes navbar-progress {
		0% { transform: translateX(-100%) scaleX(0.4); }
		50% { transform: translateX(0) scaleX(0.6); }
		100% { transform: translateX(100%) scaleX(0.4); }
	}
	@media (prefers-reduced-motion: reduce) {
		.navbar-progress { animation: none; opacity: 0.6; }
	}
</style>
