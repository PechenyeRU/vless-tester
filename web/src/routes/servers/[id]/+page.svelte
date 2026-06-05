<script>
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago, statusClass } from '$lib/format.js';

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

<a class="btn btn-ghost btn-sm mb-4" href="/servers">← Servers</a>

{#if error}
	<div class="alert alert-error mb-4"><span>{error}</span></div>
{/if}

{#if loading}
	<div class="grid place-items-center py-16">
		<span class="loading loading-spinner loading-lg text-primary"></span>
	</div>
{:else if detail}
	{@const s = detail.server}
	<h1 class="text-2xl font-bold mb-4">{flag(s.country)} {s.seq_name || s.host}</h1>

	<div class="card bg-base-100 shadow mb-6">
		<div class="card-body p-0">
			<div class="overflow-x-auto">
				<table class="table table-sm">
					<tbody>
						<tr><th class="w-32">ID</th><td>{s.id}</td></tr>
						<tr><th>Protocol</th><td><span class="badge badge-ghost badge-sm">{s.protocol}</span></td></tr>
						<tr><th>Endpoint</th><td class="mono">{s.host}:{s.port}</td></tr>
						<tr><th>Country</th><td>{flag(s.country)} {s.country || '—'}</td></tr>
						<tr><th>Seq name</th><td>{s.seq_name || '—'}</td></tr>
						<tr><th>Raw URI</th><td class="mono text-xs text-base-content/60 break-all">{s.raw_uri}</td></tr>
					</tbody>
				</table>
			</div>
		</div>
	</div>

	<h2 class="text-lg font-semibold mb-2">History <span class="text-base-content/50 font-normal">({detail.history?.length || 0} runs)</span></h2>
	<div class="card bg-base-100 shadow">
		<div class="card-body p-0">
			<div class="overflow-x-auto">
				<table class="table table-sm">
					<thead>
						<tr>
							<th>When</th><th>Worker</th><th>Phase</th><th>Latency</th>
							<th>Download</th><th>Upload</th><th>Status</th><th>Error</th>
						</tr>
					</thead>
					<tbody>
						{#each detail.history || [] as r}
							<tr class="hover">
								<td class="text-base-content/60">{ago(r.run_at)}</td>
								<td class="mono">{r.worker_id}</td>
								<td>{r.phase}</td>
								<td>{ms(r.latency_ms)}</td>
								<td>{mbps(r.dl_mbps)}</td>
								<td>{mbps(r.ul_mbps)}</td>
								<td><span class="badge badge-sm {statusClass(r.status)}">{r.status}</span></td>
								<td class="text-error text-xs max-w-md break-all">{r.error || ''}</td>
							</tr>
						{:else}
							<tr><td colspan="8" class="text-base-content/60 py-6 text-center">No runs yet.</td></tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	</div>
{/if}
