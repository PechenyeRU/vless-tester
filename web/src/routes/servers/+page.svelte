<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago, statusClass } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

	let servers = $state([]);
	let error = $state('');
	let notice = $state('');
	let loading = $state(false);
	let filter = $state({ country: '', worker: '', minSpeed: '', limit: 200 });

	// Manual add: paste one config link.
	let newServer = $state('');
	let adding = $state(false);

	// Edit modal state. editForm carries the full editable detail of one server.
	let editForm = $state(null); // { id, raw_uri, country, seq_name }
	let saving = $state(false);

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

	function flash(msg) {
		notice = msg;
		setTimeout(() => (notice = ''), 2500);
	}

	async function addServer() {
		error = '';
		const raw = newServer.trim();
		if (!raw) return;
		adding = true;
		try {
			await api.createServer({ raw_uri: raw });
			newServer = '';
			await load();
			flash('Server added');
		} catch (e) {
			error = e.message;
		} finally {
			adding = false;
		}
	}

	async function startEdit(s) {
		error = '';
		try {
			// The list summary has no raw link; fetch the detail for the full record.
			const detail = await api.server(s.id);
			editForm = {
				id: s.id,
				raw_uri: detail.server.raw_uri,
				country: detail.server.country || '',
				seq_name: detail.server.seq_name || ''
			};
		} catch (e) {
			error = e.message;
		}
	}

	async function saveEdit() {
		error = '';
		saving = true;
		try {
			await api.updateServer(editForm.id, {
				raw_uri: editForm.raw_uri.trim(),
				country: editForm.country.trim(),
				seq_name: editForm.seq_name.trim()
			});
			editForm = null;
			await load();
			flash('Server updated');
		} catch (e) {
			error = e.message;
		} finally {
			saving = false;
		}
	}

	async function removeServer(s) {
		error = '';
		const label = s.seq_name || `${s.host}:${s.port}`;
		if (!confirm(`Delete ${label}? Its measurement history is removed too.`)) return;
		try {
			await api.deleteServer(s.id);
			await load();
			flash('Server deleted');
		} catch (e) {
			error = e.message;
		}
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

<div class="card bg-base-100 shadow mb-4">
	<div class="card-body p-4">
		<form
			class="flex flex-wrap items-end gap-3"
			onsubmit={(e) => {
				e.preventDefault();
				addServer();
			}}
		>
			<label class="form-control flex-1 min-w-72">
				<span class="label-text mb-1">
					Add a node manually
					<Help tip="Paste one share link (vless, vmess, trojan, ss, hysteria2, tuic and so on). It is parsed and inserted like an ingested node; re-adding an existing endpoint is a no-op." />
				</span>
				<input class="input input-bordered input-sm w-full mono text-xs" bind:value={newServer} placeholder="vless://uuid@host:443?...#node" />
			</label>
			<button class="btn btn-primary btn-sm" type="submit" disabled={adding}>
				{#if adding}<span class="loading loading-spinner loading-xs"></span>{/if}
				Add server
			</button>
		</form>
	</div>
</div>

{#if error}
	<div class="alert alert-error mb-4"><span>{error}</span></div>
{/if}

<p class="text-sm text-base-content/60 mb-2">{loading ? 'Loading...' : `${servers.length} servers`}</p>

<div class="card bg-base-100 shadow">
	<div class="card-body p-0">
		<div class="overflow-x-auto">
			<table class="table table-sm">
				<thead>
					<tr>
						<th>Name</th><th>Proto</th><th>Endpoint</th><th>Latency</th>
						<th>Download</th><th>Status</th><th>Worker</th><th>Age</th><th></th>
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
							<td><span class="badge badge-sm {statusClass(s.status)}">{s.status || 'untested'}</span></td>
							<td class="mono text-base-content/60">{s.worker || 'none'}</td>
							<td class="text-base-content/60">{ago(s.last_run)}</td>
							<td>
								<div class="flex gap-1">
									<button class="btn btn-xs" onclick={() => startEdit(s)}>Edit</button>
									<button class="btn btn-xs btn-error btn-outline" onclick={() => removeServer(s)}>Delete</button>
								</div>
							</td>
						</tr>
					{:else}
						<tr><td colspan="9" class="text-base-content/60 py-6 text-center">No servers match.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
</div>

{#if editForm}
	<div class="modal modal-open">
		<div class="modal-box">
			<h3 class="font-bold text-lg mb-3">Edit server</h3>
			<label class="form-control mb-3">
				<span class="label-text mb-1">
					Config link
					<Help tip="The raw share link. Editing it re-parses the protocol, host, port and credential, so the node identity is recomputed." />
				</span>
				<textarea class="textarea textarea-bordered mono text-xs h-24 w-full" bind:value={editForm.raw_uri}></textarea>
			</label>
			<div class="grid gap-3 sm:grid-cols-2">
				<label class="form-control">
					<span class="label-text mb-1">Country <span class="text-base-content/50">(ISO code, e.g. FR)</span></span>
					<input class="input input-bordered input-sm" bind:value={editForm.country} placeholder="FR" />
				</label>
				<label class="form-control">
					<span class="label-text mb-1">Display name <span class="text-base-content/50">(seq name)</span></span>
					<input class="input input-bordered input-sm mono text-xs" bind:value={editForm.seq_name} placeholder="FR110" />
				</label>
			</div>
			<div class="modal-action">
				<button class="btn btn-sm" onclick={() => (editForm = null)} disabled={saving}>Cancel</button>
				<button class="btn btn-sm btn-primary" onclick={saveEdit} disabled={saving}>
					{#if saving}<span class="loading loading-spinner loading-xs"></span>{/if}
					Save
				</button>
			</div>
		</div>
		<button class="modal-backdrop" aria-label="Close" onclick={() => (editForm = null)}></button>
	</div>
{/if}

{#if notice}
	<div class="toast toast-end">
		<div class="alert alert-success">
			<span>{notice}</span>
		</div>
	</div>
{/if}
