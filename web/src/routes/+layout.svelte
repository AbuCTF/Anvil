<script lang="ts">
	import '@fontsource-variable/jetbrains-mono/wght.css';
	import '@fontsource-variable/inter/wght.css';
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { auth } from '$stores/auth';
	import Icon from '@iconify/svelte';
	import OpticalIcon from '$lib/components/OpticalIcon.svelte';
	import EventClock from '$lib/components/EventClock.svelte';
	import RankBadge from '$lib/components/RankBadge.svelte';

	let mobileMenuOpen = false;
	let userMenuOpen = false;

	const navigation = [
		{ name: 'Challenges', href: '/challenges', icon: 'mdi:flag' },
		{ name: 'Scoreboard', href: '/scoreboard', icon: 'mdi:trophy' },
		{ name: 'Arena', href: '/arena', icon: 'mdi:sword-cross' },
		{ name: 'Instances', href: '/instances', icon: 'mdi:server' }
	];

	const userMenu = [
		{ name: 'Profile', href: '/profile', icon: 'mdi:account' },
		{ name: 'My Instances', href: '/instances', icon: 'mdi:server' },
		{ name: 'VPN', href: '/vpn', icon: 'mdi:vpn' }
	];

	const iconMetrics: Record<string, { size: number }> = {
		'mdi:flag': { size: 15.5 },
		'mdi:trophy': { size: 13.5 },
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

	onMount(() => {
		// Check for existing auth token
		auth.checkAuth();
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

<div class="min-h-screen bg-stone-950 text-stone-100 flex flex-col">
	<!-- Navigation -->
	<nav class="border-b border-stone-800 bg-stone-950/95 backdrop-blur-sm sticky top-0 z-50">
		<div class="w-full px-4 sm:px-6 lg:px-8 2xl:px-10">
			<div class="grid h-16 grid-cols-[auto_1fr_auto] items-center lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
				<!-- Logo / wordmark -->
				<a href="/" class="col-start-1 row-start-1 flex shrink-0 items-center justify-self-start">
					<img src="/logo.png" alt="Anvil" class="h-10 w-auto" />
				</a>

				<!-- Desktop Navigation - Centered -->
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

				<!-- Event and account utilities -->
				<div class="col-start-3 row-start-1 flex min-w-0 shrink-0 items-center justify-self-end gap-1 lg:gap-2 xl:gap-3">
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
								<div class="absolute right-0 mt-2 w-52 bg-stone-950 backdrop-blur-md border border-stone-800 rounded-lg z-50 overflow-hidden">
									<div class="px-4 py-3 border-b border-stone-800">
										<p class="text-sm font-medium text-stone-100">{$auth.user?.username}</p>
										<p class="text-xs text-stone-500">{$auth.user?.email || 'No email'}</p>
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
								href="/register"
								class="rounded-full bg-amber-500 px-5 py-2 text-sm font-semibold text-amber-950 transition-colors hover:bg-amber-400"
							>
								Register
							</a>
						{/if}
					</div>
					<!-- Mobile menu button -->
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

		<!-- Mobile menu -->
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
							<a href="/register" on:click={() => mobileMenuOpen = false} class="flex-1 px-4 py-2.5 text-center text-sm font-semibold text-amber-950 bg-amber-500 rounded-full hover:bg-amber-400 transition-colors">Register</a>
						</div>
					{/if}
				</div>
			</div>
		{/if}
	</nav>

	<!-- Main content -->
	<main class="flex-1">
		<slot />
	</main>
</div>
