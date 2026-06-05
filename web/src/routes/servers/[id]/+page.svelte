<script>
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago, statusClass } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

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

	{#if detail.checks?.length}
		{@const ipRisk = detail.checks.find((c) => c.name === 'ip_risk')}
		{@const media = detail.checks.filter((c) => c.name !== 'ip_risk')}
		{#if ipRisk}
			<h2 class="text-lg font-semibold mb-2">
				IP risk
				<Help tip="Reputation of the node's exit IP — lower is better. ~0 = clean/residential, ~25 = datacenter, ≥50 = flagged proxy/VPN (likely to be blocked by anti-fraud)." />
			</h2>
			<div class="card bg-base-100 shadow mb-6">
				<div class="card-body flex-row items-center gap-3">
					<span
						class="badge badge-lg {(ipRisk.metric ?? 0) >= 50
							? 'badge-error'
							: (ipRisk.metric ?? 0) > 0
								? 'badge-warning'
								: 'badge-success'}"
					>
						{Math.round(ipRisk.metric ?? 0)}% risk
					</span>
					{#if ipRisk.detail}
						<span class="text-sm text-base-content/60">{ipRisk.detail}</span>
					{/if}
				</div>
			</div>
		{/if}
		{#if media.length}
			<h2 class="text-lg font-semibold mb-2">
				Media unlock
				<Help tip="Whether each streaming/AI service works through this node (green = unlocked). The region in the detail feeds the public name tag, e.g. GPT⁺-US." />
			</h2>
			<div class="card bg-base-100 shadow mb-6">
				<div class="card-body flex-row flex-wrap gap-2">
					{#each media as c}
						<span class="badge gap-1 {c.passed ? 'badge-success' : 'badge-ghost'}" title={c.detail}>
							{c.name}{#if c.detail} · {c.detail}{/if}
						</span>
					{/each}
				</div>
			</div>
		{/if}
	{/if}

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
