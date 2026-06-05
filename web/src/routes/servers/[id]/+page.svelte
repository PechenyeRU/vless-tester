<script>
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago } from '$lib/format.js';

	let detail = $state(null);
	let error = $state('');
	let loading = $state(true);

	async function load() {
		loading = true;
		error = '';
		try {
			detail = await api.server($page.params.id);
		} catch (e) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<p><a class="link" href="/servers">← servers</a></p>

{#if error}<p class="error">{error}</p>{/if}
{#if loading}
	<p class="muted">Loading…</p>
{:else if detail}
	{@const s = detail.server}
	<h1>{flag(s.country)} {s.seq_name || s.host}</h1>
	<table>
		<tbody>
			<tr><th>id</th><td>{s.id}</td></tr>
			<tr><th>protocol</th><td>{s.protocol}</td></tr>
			<tr><th>endpoint</th><td>{s.host}:{s.port}</td></tr>
			<tr><th>country</th><td>{s.country || '—'}</td></tr>
			<tr><th>seq name</th><td>{s.seq_name || '—'}</td></tr>
			<tr><th>raw uri</th><td class="muted" style="word-break:break-all">{s.raw_uri}</td></tr>
		</tbody>
	</table>

	<h2>History ({detail.history?.length || 0} runs)</h2>
	<table>
		<thead>
			<tr><th>when</th><th>worker</th><th>phase</th><th>latency</th><th>dl</th><th>ul</th><th>status</th><th>error</th></tr>
		</thead>
		<tbody>
			{#each detail.history || [] as r}
				<tr>
					<td class="muted">{ago(r.run_at)}</td>
					<td>{r.worker_id}</td>
					<td>{r.phase}</td>
					<td>{ms(r.latency_ms)}</td>
					<td>{mbps(r.dl_mbps)}</td>
					<td>{mbps(r.ul_mbps)}</td>
					<td class:ok={r.status === 'ok'} class:bad={r.status !== 'ok'}>{r.status}</td>
					<td class="bad">{r.error || ''}</td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}
