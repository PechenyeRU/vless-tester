<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago, statusClass } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

	let servers = $state([]);
	let error = $state('');
	let loading = $state(false);
	let filter = $state({ country: '', worker: '', minSpeed: '', limit: 200 });

	async function load() {
		loading = true;
		error = '';
		try {
			servers =
				(await api.servers({
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

	function reset() {
		filter = { country: '', worker: '', minSpeed: '', limit: 200 };
		load();
	}

	onMount(load);
</script>

<h1 class="text-2xl font-bold mb-4">
	Servers
	<Help tip="Every ingested node and its latest measurement. Filter by country (e.g. FR), worker, or minimum download speed. Click a name for per-worker history, media unlock and IP risk." pos="bottom" />
</h1>

<div class="card bg-base-100 shadow mb-4">
	<div class="card-body p-4">
		<form
			class="flex flex-wrap items-end gap-3"
			onsubmit={(e) => {
				e.preventDefault();
				load();
			}}
		>
			<label class="form-control">
				<span class="label-text mb-1">Country</span>
				<input class="input input-bordered input-sm w-24" bind:value={filter.country} placeholder="FR" />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Worker</span>
				<input class="input input-bordered input-sm w-44" bind:value={filter.worker} placeholder="swift-otter-1" />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Min dl (MB/s)</span>
				<input class="input input-bordered input-sm w-28" type="number" step="0.1" bind:value={filter.minSpeed} />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Limit</span>
				<input class="input input-bordered input-sm w-24" type="number" bind:value={filter.limit} />
			</label>
			<button class="btn btn-primary btn-sm" type="submit">
				{#if loading}<span class="loading loading-spinner loading-xs"></span>{/if}
				Filter
			</button>
			<button class="btn btn-ghost btn-sm" type="button" onclick={reset}>Reset</button>
		</form>
	</div>
</div>

{#if error}
	<div class="alert alert-error mb-4"><span>{error}</span></div>
{/if}

<p class="text-sm text-base-content/60 mb-2">{loading ? 'Loading…' : `${servers.length} servers`}</p>

<div class="card bg-base-100 shadow">
	<div class="card-body p-0">
		<div class="overflow-x-auto">
			<table class="table table-sm">
				<thead>
					<tr>
						<th>Name</th><th>Proto</th><th>Endpoint</th><th>Latency</th>
						<th>Download</th><th>Status</th><th>Worker</th><th>Age</th>
					</tr>
				</thead>
				<tbody>
					{#each servers as s}
						<tr class="hover">
							<td>
								<a class="link link-primary font-medium" href="/servers/{s.id}">
									{flag(s.country)} {s.seq_name || s.country || '?'}
								</a>
							</td>
							<td><span class="badge badge-ghost badge-sm">{s.protocol}</span></td>
							<td class="mono text-base-content/60">{s.host}:{s.port}</td>
							<td>{ms(s.latency_ms)}</td>
							<td class="font-medium">{mbps(s.dl_mbps)}</td>
							<td><span class="badge badge-sm {statusClass(s.status)}">{s.status || '—'}</span></td>
							<td class="mono text-base-content/60">{s.worker || '—'}</td>
							<td class="text-base-content/60">{ago(s.last_run)}</td>
						</tr>
					{:else}
						<tr><td colspan="8" class="text-base-content/60 py-6 text-center">No servers match.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
</div>
