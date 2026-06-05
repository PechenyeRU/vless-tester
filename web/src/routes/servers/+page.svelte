<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago } from '$lib/format.js';

	let servers = $state([]);
	let error = $state('');
	let loading = $state(false);
	let filter = $state({ country: '', worker: '', minSpeed: '', limit: 200 });

	async function load() {
		loading = true;
		error = '';
		try {
			servers = (await api.servers({
				country: filter.country.trim(),
				worker: filter.worker.trim(),
				minSpeed: Number(filter.minSpeed) || 0,
				limit: Number(filter.limit) || 200
			})) || [];
		} catch (e) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<h1>Servers</h1>

<form class="row" onsubmit={(e) => { e.preventDefault(); load(); }}>
	<label class="field">country<input bind:value={filter.country} placeholder="FR" size="6" /></label>
	<label class="field">worker<input bind:value={filter.worker} placeholder="swift-otter-1" /></label>
	<label class="field">min dl (MB/s)<input bind:value={filter.minSpeed} type="number" step="0.1" size="6" /></label>
	<label class="field">limit<input bind:value={filter.limit} type="number" size="6" /></label>
	<button class="primary" type="submit">Filter</button>
</form>

{#if error}<p class="error">{error}</p>{/if}

<p class="muted">{loading ? 'Loading…' : `${servers.length} servers`}</p>

<table>
	<thead>
		<tr><th>name</th><th>proto</th><th>host</th><th>latency</th><th>dl</th><th>status</th><th>worker</th><th>age</th></tr>
	</thead>
	<tbody>
		{#each servers as s}
			<tr>
				<td><a class="link" href="/servers/{s.id}">{flag(s.country)} {s.seq_name || s.country || '?'}</a></td>
				<td>{s.protocol}</td>
				<td class="muted">{s.host}:{s.port}</td>
				<td>{ms(s.latency_ms)}</td>
				<td>{mbps(s.dl_mbps)}</td>
				<td class:ok={s.status === 'ok'} class:bad={s.status && s.status !== 'ok'}>{s.status || '—'}</td>
				<td class="muted">{s.worker || '—'}</td>
				<td class="muted">{ago(s.last_run)}</td>
			</tr>
		{/each}
	</tbody>
</table>
