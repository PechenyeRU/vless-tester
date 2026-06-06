<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ms, ago, statusClass } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

	let servers = $state([]);
	let total = $state(0);
	let error = $state('');
	let notice = $state('');
	let loading = $state(false);

	// Query state: real-time search, column sort, pagination.
	let search = $state('');
	let sort = $state('speed');
	let dir = $state('desc');
	let page = $state(1);
	let perPage = $state(50);
	let pages = $derived(Math.max(1, Math.ceil(total / perPage)));

	let searchTimer;

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
			const res =
				(await api.servers({
					q: search.trim(),
					sort,
					dir,
					page,
					perPage
				})) || {};
			servers = res.servers || [];
			total = res.total || 0;
		} catch (e) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	// Debounced live search: typing resets to the first page and reloads.
	function onSearchInput() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			page = 1;
			load();
		}, 300);
	}

	// defaultDir picks a sensible initial direction per column: high-is-good
	// numeric/time columns descend, text columns ascend.
	function defaultDir(col) {
		return ['speed', 'last_run'].includes(col) ? 'desc' : 'asc';
	}

	function sortBy(col) {
		if (sort === col) {
			dir = dir === 'asc' ? 'desc' : 'asc';
		} else {
			sort = col;
			dir = defaultDir(col);
		}
		page = 1;
		load();
	}

	function go(p) {
		if (p >= 1 && p <= pages && p !== page) {
			page = p;
			load();
		}
	}

	function changePerPage(n) {
		perPage = n;
		page = 1;
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

<div class="flex flex-wrap items-center gap-3 mb-4">
	<label class="input input-bordered input-sm flex items-center gap-2 flex-1 min-w-64 max-w-md">
		<span class="text-base-content/50">🔍</span>
		<input
			class="grow"
			type="search"
			placeholder="Search host, name or country…"
			bind:value={search}
			oninput={onSearchInput}
		/>
		{#if loading}<span class="loading loading-spinner loading-xs"></span>{/if}
	</label>
	<label class="flex items-center gap-2 text-sm text-base-content/60">
		Per page
		<select class="select select-bordered select-sm" value={perPage} onchange={(e) => changePerPage(Number(e.currentTarget.value))}>
			<option value={25}>25</option>
			<option value={50}>50</option>
			<option value={100}>100</option>
			<option value={200}>200</option>
		</select>
	</label>
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

{#snippet sortable(label, col)}
	<th class="cursor-pointer select-none hover:text-base-content" onclick={() => sortBy(col)}>
		{label}{#if sort === col}<span class="ml-0.5">{dir === 'asc' ? '▲' : '▼'}</span>{/if}
	</th>
{/snippet}

<p class="text-sm text-base-content/60 mb-2">
	{loading ? 'Loading…' : `${total.toLocaleString()} servers`}{#if total > 0} · page {page} of {pages}{/if}
</p>

<div class="card bg-base-100 shadow">
	<div class="card-body p-0">
		<div class="overflow-x-auto">
			<table class="table table-sm">
				<thead>
					<tr>
						{@render sortable('Name', 'seq')}
						{@render sortable('Proto', 'protocol')}
						{@render sortable('Endpoint', 'host')}
						{@render sortable('Latency', 'latency')}
						{@render sortable('Download', 'speed')}
						{@render sortable('Status', 'status')}
						<th>Worker</th>
						{@render sortable('Age', 'last_run')}
						<th></th>
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

{#if pages > 1}
	<div class="flex items-center justify-center gap-2 mt-4">
		<button class="btn btn-sm" disabled={page <= 1 || loading} onclick={() => go(1)}>«</button>
		<button class="btn btn-sm" disabled={page <= 1 || loading} onclick={() => go(page - 1)}>‹ Prev</button>
		<span class="text-sm text-base-content/70 px-2">Page {page} / {pages}</span>
		<button class="btn btn-sm" disabled={page >= pages || loading} onclick={() => go(page + 1)}>Next ›</button>
		<button class="btn btn-sm" disabled={page >= pages || loading} onclick={() => go(pages)}>»</button>
	</div>
{/if}

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
