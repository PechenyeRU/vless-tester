<script>
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { auth } from '$lib/auth.svelte.js';
	import { onUnauthorized } from '$lib/api.js';
	import './app.css';

	let { children } = $props();

	const nav = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/servers', label: 'Servers' },
		{ href: '/admin', label: 'Admin' }
	];

	let theme = $state('dim');

	onMount(() => {
		// Drop the session and return to login whenever the API rejects the token.
		onUnauthorized(() => {
			auth.logout();
			goto('/login');
		});
		theme = localStorage.getItem('theme') || 'dim';
		document.documentElement.dataset.theme = theme;
	});

	function toggleTheme() {
		theme = theme === 'dim' ? 'nord' : 'dim';
		document.documentElement.dataset.theme = theme;
		if (browser) localStorage.setItem('theme', theme);
	}

	function logout() {
		auth.logout();
		goto('/login');
	}

	const path = $derived($page.url.pathname);
	const isLogin = $derived(path === '/login');

	// Route guard: bounce unauthenticated users to the login page.
	$effect(() => {
		if (!isLogin && !auth.isAuthed) goto('/login');
	});

	function active(href) {
		return href === '/' ? path === '/' : path.startsWith(href);
	}
</script>

{#if isLogin}
	{@render children()}
{:else if auth.isAuthed}
	<div class="navbar bg-base-100 border-b border-base-300 px-4 sticky top-0 z-30">
		<div class="flex-1 items-center gap-4">
			<a href="/" class="text-lg font-bold text-primary">vless-tester</a>
			<ul class="menu menu-horizontal gap-1 hidden sm:flex">
				{#each nav as item}
					<li>
						<a href={item.href} class:active={active(item.href)}>{item.label}</a>
					</li>
				{/each}
			</ul>
		</div>
		<div class="flex-none gap-2">
			<button class="btn btn-ghost btn-sm" onclick={toggleTheme} aria-label="toggle theme">
				{theme === 'dim' ? '☾' : '☀'}
			</button>
			<button class="btn btn-ghost btn-sm" onclick={logout}>Sign out</button>
		</div>
	</div>

	<!-- Compact nav for small screens -->
	<ul class="menu menu-horizontal gap-1 flex sm:hidden bg-base-100 border-b border-base-300 px-2">
		{#each nav as item}
			<li><a href={item.href} class:active={active(item.href)}>{item.label}</a></li>
		{/each}
	</ul>

	<main class="max-w-6xl mx-auto p-4 sm:p-6">
		{@render children()}
	</main>
{:else}
	<div class="min-h-screen grid place-items-center">
		<span class="loading loading-spinner loading-lg text-primary"></span>
	</div>
{/if}
