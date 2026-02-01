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
	<nav class="border-b border-stone-800 bg-black">
		<div class="max-w-7xl mx-auto px-6 sm:px-8">
			<div class="flex items-center justify-between h-16">
				<!-- Logo -->
				<div class="flex items-center">
				<a href="/" class="flex items-center">
					<img src="/logo.png" alt="Anvil" class="h-12 w-auto" />
				</a>
			</div>

			<!-- Desktop Navigation -->
			<div class="hidden md:flex items-center space-x-2">
				{#each navigation as item}
					<a
						href={item.href}
						class="flex items-center gap-2 px-4 py-2 text-sm font-medium transition-colors rounded-lg
                     {$page.url.pathname.startsWith(item.href)
							? 'text-white bg-stone-800/50'
							: 'text-stone-400 hover:text-white hover:bg-stone-900/50'}"
					>
						<Icon icon={item.icon} class="w-4 h-4" />
						{item.name}
					</a>
				{/each}
			</div>

				<!-- User Menu -->
				<div class="hidden md:flex items-center space-x-2">
					{#if $auth.isAuthenticated}
						{#if $auth.user?.role === 'admin'}
							<a
								href="/admin"
								class="flex items-center space-x-1.5 px-3 py-1.5 text-sm font-mono text-accent hover:text-accent-light"
							>
								<Icon icon="mdi:shield-crown" class="w-4 h-4" />
								<span>Admin</span>
							</a>
						{/if}
						<div class="relative">
							<button
								on:click={() => userMenuOpen = !userMenuOpen}
								class="flex items-center space-x-2 px-3 py-1.5 text-sm font-mono text-stone-300 hover:text-white transition-colors rounded-lg hover:bg-stone-900/50"
							>
								<div class="w-7 h-7 bg-gradient-to-br from-stone-700 to-stone-900 border border-stone-700/50 rounded-lg flex items-center justify-center">
									<span class="text-xs font-semibold text-white">
										{($auth.user?.username || 'U').charAt(0).toUpperCase()}
									</span>
								</div>
								<span>{$auth.user?.username}</span>
								<Icon icon="mdi:chevron-down" class="w-3 h-3" />
							</button>

							{#if userMenuOpen}
								<div class="absolute right-0 mt-2 w-48 bg-stone-950/95 backdrop-blur-sm border border-stone-800/50 rounded-xl shadow-2xl z-50 overflow-hidden">
									{#each userMenu as item}
										<a
											href={item.href}
											on:click={() => userMenuOpen = false}
											class="flex items-center space-x-2 px-3 py-2 text-sm font-mono text-stone-400 hover:bg-stone-800 hover:text-stone-200"
										>
											<Icon icon={item.icon} class="w-4 h-4" />
											<span>{item.name}</span>
										</a>
									{/each}
									<hr class="border-stone-800/50" />
									<button
										on:click={handleLogout}
										class="flex items-center space-x-3 px-4 py-2.5 text-sm text-stone-400 hover:bg-stone-900/50 hover:text-red-400 transition-colors w-full text-left"
									>
										<Icon icon="mdi:logout" class="w-4 h-4" />
										<span>Logout</span>
									</button>
								</div>
							{/if}
						</div>
					{:else}
						<a
							href="/login"
							class="px-4 py-2 text-base font-mono font-medium text-stone-400 hover:text-white"
						>
							Login
						</a>
						<a
							href="/register"
							class="px-6 py-2 border border-stone-600 text-base font-mono font-medium text-white hover:bg-white hover:text-black transition-colors rounded-full"
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
			<div class="md:hidden border-t border-stone-800">
				<div class="px-2 pt-2 pb-3 space-y-1">
					{#each navigation as item}
						<a
							href={item.href}
							class="flex items-center space-x-2 px-3 py-2 text-sm font-mono
                     {$page.url.pathname.startsWith(item.href)
								? 'text-stone-100 bg-stone-900'
								: 'text-stone-400 hover:bg-stone-900 hover:text-stone-200'}"
						>
							<Icon icon={item.icon} class="w-4 h-4" />
							<span>{item.name}</span>
						</a>
					{/each}
				</div>
			</div>
		{/if}
	</nav>

	<!-- Main content -->
	<main class="flex-1">
		<slot />
	</main>
</div>
