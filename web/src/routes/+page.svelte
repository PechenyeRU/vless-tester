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

	const aliveCount = $derived(workers.filter((w) => w.status !== 'dead').length);

	onMount(load);
</script>

<div class="flex items-center justify-between mb-4">
	<h1 class="text-2xl font-bold">Dashboard</h1>
	<button class="btn btn-sm btn-ghost" onclick={load} disabled={loading}>
		{#if loading}<span class="loading loading-spinner loading-xs"></span>{/if}
		Refresh
	</button>
</div>

{#if error}
	<div class="alert alert-error mb-4"><span>{error}</span></div>
{/if}

{#if loading && !stats}
	<div class="grid place-items-center py-16">
		<span class="loading loading-spinner loading-lg text-primary"></span>
	</div>
{:else if stats}
	<div class="stats stats-vertical sm:stats-horizontal shadow w-full bg-base-100 mb-6">
		<div class="stat">
			<div class="stat-title">Servers</div>
			<div class="stat-value text-primary">{stats.servers}</div>
		</div>
		<div class="stat">
			<div class="stat-title">Test runs</div>
			<div class="stat-value">{stats.runs}</div>
		</div>
		<div class="stat">
			<div class="stat-title">Workers</div>
			<div class="stat-value">
				{aliveCount}<span class="text-base font-normal text-base-content/50">/{workers.length}</span>
			</div>
			<div class="stat-desc">alive / total</div>
		</div>
		<div class="stat">
			<div class="stat-title">Countries</div>
			<div class="stat-value">{(stats.by_country || []).length}</div>
		</div>
	</div>

	<div class="card bg-base-100 shadow mb-6">
		<div class="card-body p-0">
			<h2 class="text-lg font-semibold px-5 pt-4">Fleet</h2>
			{#if workers.length === 0}
				<p class="text-base-content/60 px-5 py-6">No workers registered.</p>
			{:else}
				<div class="overflow-x-auto">
					<table class="table table-sm">
						<thead>
							<tr>
								<th>Worker</th><th>Status</th><th>Latency cap</th><th>Speed cap</th>
								<th>Bandwidth</th><th>Last seen</th>
							</tr>
						</thead>
						<tbody>
							{#each workers as w}
								<tr class="hover">
									<td class="mono font-medium">{w.id}</td>
									<td>
										<span class="badge badge-sm {w.status === 'dead' ? 'badge-error' : 'badge-success'}">
											{w.status}
										</span>
									</td>
									<td>{w.capacity?.latency ?? '—'}</td>
									<td>{w.capacity?.speed ?? '—'}</td>
									<td>{w.capacity?.bw_mbps ? w.capacity.bw_mbps.toFixed(1) + ' MB/s' : '—'}</td>
									<td class="text-base-content/60">{ago(w.last_seen)}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>
	</div>

	<div class="grid gap-6 lg:grid-cols-2">
		<div class="card bg-base-100 shadow">
			<div class="card-body p-0">
				<h2 class="text-lg font-semibold px-5 pt-4">By country</h2>
				<div class="overflow-x-auto">
					<table class="table table-sm">
						<thead>
							<tr><th>Country</th><th>Servers</th><th>Tested ok</th><th>Median dl</th></tr>
						</thead>
						<tbody>
							{#each stats.by_country || [] as c}
								<tr class="hover">
									<td>{flag(c.country)} {c.country || '—'}</td>
									<td>{c.servers}</td>
									<td>{c.tested}</td>
									<td class="font-medium">{mbps(c.median_dl_mbps)}</td>
								</tr>
							{:else}
								<tr><td colspan="4" class="text-base-content/60">No data yet.</td></tr>
							{/each}
						</tbody>
					</table>
				</div>
			</div>
		</div>

		<div class="card bg-base-100 shadow">
			<div class="card-body p-0">
				<h2 class="text-lg font-semibold px-5 pt-4">By worker</h2>
				<div class="overflow-x-auto">
					<table class="table table-sm">
						<thead>
							<tr><th>Worker</th><th>Runs</th><th>Ok</th><th>Last seen</th></tr>
						</thead>
						<tbody>
							{#each stats.by_worker || [] as w}
								<tr class="hover">
									<td class="mono">{w.worker_id}</td>
									<td>{w.runs}</td>
									<td>{w.ok}</td>
									<td class="text-base-content/60">{ago(w.last_seen)}</td>
								</tr>
							{:else}
								<tr><td colspan="4" class="text-base-content/60">No data yet.</td></tr>
							{/each}
						</tbody>
					</table>
				</div>
			</div>
		</div>
	</div>
{/if}
