<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { auth } from '$stores/auth';
	import Icon from '@iconify/svelte';

	let mobileMenuOpen = false;
	let userMenuOpen = false;

	const navigation = [
		{ name: 'Challenges', href: '/challenges', icon: 'mdi:flag' },
		{ name: 'Scoreboard', href: '/scoreboard', icon: 'mdi:trophy' },
		{ name: 'Instances', href: '/instances', icon: 'mdi:server' }
	];

	const userMenu = [
		{ name: 'Profile', href: '/profile', icon: 'mdi:account' },
		{ name: 'My Instances', href: '/instances', icon: 'mdi:server' },
		{ name: 'VPN', href: '/vpn', icon: 'mdi:vpn' }
	];

	onMount(() => {
		// Check for existing auth token
		auth.checkAuth();
	});

	function handleLogout() {
		auth.logout();
		userMenuOpen = false;
	}
</script>

<div class="min-h-screen bg-black text-stone-100 flex flex-col">
	<!-- Navigation -->
	<nav class="border-b border-stone-800 bg-black/95 backdrop-blur-sm sticky top-0 z-50">
		<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
			<div class="flex items-center justify-between h-16">
				<!-- Logo / wordmark -->
				<a href="/" class="flex items-center shrink-0">
					<img src="/logo.png" alt="Anvil" class="h-10 w-auto" />
				</a>

				<!-- Desktop Navigation - Centered -->
				<div class="hidden md:flex items-center justify-center flex-1 px-8">
					<div class="flex items-center gap-1">
						{#each navigation as item}
							<a
								href={item.href}
								class="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium rounded-md transition-colors
								{$page.url.pathname.startsWith(item.href)
									? 'text-amber-500'
									: 'text-stone-400 hover:text-stone-100 hover:bg-stone-800/40'}"
							>
								<Icon icon={item.icon} class="w-4 h-4" />
								<span>{item.name}</span>
							</a>
						{/each}
					</div>
				</div>

				<!-- User Menu -->
				<div class="hidden md:flex items-center gap-3 shrink-0">
					{#if $auth.isAuthenticated}
						{#if $auth.user?.role === 'admin'}
							<a
								href="/admin"
								class="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-amber-500 hover:text-amber-400 transition-colors"
							>
								<Icon icon="mdi:shield-crown" class="w-4 h-4" />
								<span>Admin</span>
							</a>
						{/if}
						<div class="relative">
							<button
								on:click={() => userMenuOpen = !userMenuOpen}
								class="flex items-center gap-2 px-2 py-1.5 text-sm text-stone-300 hover:text-stone-100 transition-colors rounded-md hover:bg-stone-800/40"
							>
								<div class="w-8 h-8 bg-stone-800 border border-stone-700 rounded-md flex items-center justify-center">
									<span class="text-xs font-semibold text-stone-300">
										{($auth.user?.username || 'U').charAt(0).toUpperCase()}
									</span>
								</div>
								<span class="font-medium">{$auth.user?.username}</span>
								<Icon icon="mdi:chevron-down" class="w-4 h-4 text-stone-500" />
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
											class="flex items-center gap-3 px-4 py-2.5 text-sm text-stone-400 hover:bg-stone-800/40 hover:text-stone-100 transition-colors"
										>
											<Icon icon={item.icon} class="w-4 h-4" />
											<span>{item.name}</span>
										</a>
									{/each}
									<div class="border-t border-stone-800">
										<button
											on:click={handleLogout}
											class="flex items-center gap-3 px-4 py-2.5 text-sm text-stone-400 hover:bg-stone-800/40 hover:text-danger transition-colors w-full text-left"
										>
											<Icon icon="mdi:logout" class="w-4 h-4" />
											<span>Logout</span>
										</button>
									</div>
								</div>
							{/if}
						</div>
					{:else}
						<a
							href="/login"
							class="px-3 py-2 text-sm font-medium text-stone-400 hover:text-stone-100 transition-colors"
						>
							Login
						</a>
						<a
							href="/register"
							class="px-5 py-2 bg-stone-100 text-sm font-medium text-stone-950 hover:bg-white transition-colors rounded-md"
						>
							Register
						</a>
					{/if}
				</div>

				<!-- Mobile menu button -->
				<div class="md:hidden">
					<button
						on:click={() => (mobileMenuOpen = !mobileMenuOpen)}
						class="p-2 text-stone-400 hover:text-stone-100"
					>
						<Icon icon={mobileMenuOpen ? 'mdi:close' : 'mdi:menu'} class="w-5 h-5" />
					</button>
				</div>
			</div>
		</div>

		<!-- Mobile menu -->
		{#if mobileMenuOpen}
			<div class="md:hidden border-t border-stone-800 bg-stone-950/95 backdrop-blur-md">
				<div class="px-4 py-3 space-y-1">
					{#each navigation as item}
						<a
							href={item.href}
							on:click={() => mobileMenuOpen = false}
							class="flex items-center gap-3 px-3 py-2.5 text-sm font-medium rounded-md transition-colors
							{$page.url.pathname.startsWith(item.href)
								? 'text-amber-500'
								: 'text-stone-400 hover:bg-stone-800/40 hover:text-stone-100'}"
						>
							<Icon icon={item.icon} class="w-5 h-5" />
							<span>{item.name}</span>
						</a>
					{/each}

					{#if $auth.isAuthenticated}
						<div class="border-t border-stone-800 pt-3 mt-3">
							{#each userMenu as item}
								<a
									href={item.href}
									on:click={() => mobileMenuOpen = false}
									class="flex items-center gap-3 px-3 py-2.5 text-sm font-medium text-stone-400 hover:bg-stone-800/40 hover:text-stone-100 rounded-md transition-colors"
								>
									<Icon icon={item.icon} class="w-5 h-5" />
									<span>{item.name}</span>
								</a>
							{/each}
							<button
								on:click={() => { handleLogout(); mobileMenuOpen = false; }}
								class="flex items-center gap-3 px-3 py-2.5 text-sm font-medium text-stone-400 hover:bg-stone-800/40 hover:text-danger rounded-md transition-colors w-full text-left"
							>
								<Icon icon="mdi:logout" class="w-5 h-5" />
								<span>Logout</span>
							</button>
						</div>
					{:else}
						<div class="border-t border-stone-800 pt-3 mt-3 flex gap-3">
							<a href="/login" on:click={() => mobileMenuOpen = false} class="flex-1 px-4 py-2.5 text-center text-sm font-medium text-stone-300 border border-stone-800 rounded-md hover:bg-stone-800/40 hover:text-stone-100 transition-colors">Login</a>
							<a href="/register" on:click={() => mobileMenuOpen = false} class="flex-1 px-4 py-2.5 text-center text-sm font-medium text-stone-950 bg-stone-100 rounded-md hover:bg-white transition-colors">Register</a>
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

	<!-- Footer -->
	<footer class="border-t border-stone-800">
		<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6 flex items-center justify-between">
			<span class="text-xs font-mono uppercase tracking-widest text-stone-600">Anvil</span>
			<span class="text-xs font-mono text-stone-600 tabular-nums">© 2026</span>
		</div>
	</footer>
</div>
