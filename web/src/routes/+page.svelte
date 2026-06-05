<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ago } from '$lib/format.js';

	let stats = $state(null);
	let workers = $state([]);
	let error = $state('');
	let loading = $state(true);

	async function load() {
		loading = true;
		error = '';
		try {
			[stats, workers] = await Promise.all([api.stats(), api.workers()]);
		} catch (e) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<h1>Dashboard</h1>

{#if error}
	<p class="error">{error}</p>
{/if}

{#if loading}
	<p class="muted">Loading…</p>
{:else if stats}
	<div class="cards">
		<div class="card"><div class="num">{stats.servers}</div><div class="label">servers</div></div>
		<div class="card"><div class="num">{stats.runs}</div><div class="label">test runs</div></div>
		<div class="card"><div class="num">{workers.length}</div><div class="label">workers</div></div>
		<div class="card">
			<div class="num">{(stats.by_country || []).length}</div>
			<div class="label">countries</div>
		</div>
	</div>

	<h2>Fleet</h2>
	{#if workers.length === 0}
		<p class="muted">No workers registered.</p>
	{:else}
		<table>
			<thead>
				<tr><th>worker</th><th>status</th><th>latency cap</th><th>speed cap</th><th>bw</th><th>last seen</th></tr>
			</thead>
			<tbody>
				{#each workers as w}
					<tr>
						<td>{w.id}</td>
						<td>{w.status}</td>
						<td>{w.capacity?.latency ?? '—'}</td>
						<td>{w.capacity?.speed ?? '—'}</td>
						<td>{w.capacity?.bw_mbps ? w.capacity.bw_mbps.toFixed(1) : '—'}</td>
						<td>{ago(w.last_seen)}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}

	<h2>By country</h2>
	<table>
		<thead>
			<tr><th>country</th><th>servers</th><th>tested ok</th><th>median dl</th></tr>
		</thead>
		<tbody>
			{#each stats.by_country || [] as c}
				<tr>
					<td>{flag(c.country)} {c.country}</td>
					<td>{c.servers}</td>
					<td>{c.tested}</td>
					<td>{mbps(c.median_dl_mbps)}</td>
				</tr>
			{/each}
		</tbody>
	</table>

	<h2>By worker</h2>
	<table>
		<thead>
			<tr><th>worker</th><th>runs</th><th>ok</th><th>last seen</th></tr>
		</thead>
		<tbody>
			{#each stats.by_worker || [] as w}
				<tr>
					<td>{w.worker_id}</td>
					<td>{w.runs}</td>
					<td>{w.ok}</td>
					<td>{ago(w.last_seen)}</td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}
