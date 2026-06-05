<script>
	import { page } from '$app/stores';
	import { getToken, setToken } from '$lib/api.js';
	import './app.css';

	let { children } = $props();
	let token = $state(getToken());

	function save() {
		setToken(token);
	}

	const nav = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/servers', label: 'Servers' },
		{ href: '/admin', label: 'Admin' }
	];

	function active(href) {
		const p = $page.url.pathname;
		return href === '/' ? p === '/' : p.startsWith(href);
	}
</script>

<header>
	<nav>
		<span class="brand">vless-tester</span>
		{#each nav as item}
			<a href={item.href} class:active={active(item.href)}>{item.label}</a>
		{/each}
	</nav>
	<label class="token">
		<span>admin token</span>
		<input type="password" bind:value={token} onchange={save} placeholder="bearer token" />
	</label>
</header>

<main>
	{@render children()}
</main>
