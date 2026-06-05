<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { ago } from '$lib/format.js';

	let sources = $state([]);
	let settings = $state({}); // key -> string (raw JSON text, editable)
	let tokens = $state([]);
	let error = $state('');
	let notice = $state('');
	let newSource = $state({ kind: 'subscription_url', location: '' });
	let newTokenName = $state('');
	// The freshly minted secret, shown once after creation.
	let freshToken = $state(null); // { name, token }
	let busyAction = $state('');

	async function load() {
		error = '';
		try {
			const [srcs, sett, toks] = await Promise.all([
				api.sources(),
				api.settings(),
				api.workerTokens()
			]);
			sources = srcs || [];
			tokens = toks || [];
			settings = Object.fromEntries(
				Object.entries(sett || {}).map(([k, v]) => [k, JSON.stringify(v)])
			);
		} catch (e) {
			error = e.message;
		}
	}

	async function createToken() {
		error = '';
		const name = newTokenName.trim();
		if (!name) return;
		try {
			freshToken = await api.createWorkerToken(name);
			newTokenName = '';
			await load();
		} catch (e) {
			error = e.message;
		}
	}

	async function revokeToken(tok) {
		error = '';
		if (!confirm(`Revoke the token for "${tok.name}"? That worker can no longer connect.`)) return;
		try {
			await api.revokeWorkerToken(tok.id);
			await load();
			flash(`Revoked ${tok.name}`);
		} catch (e) {
			error = e.message;
		}
	}

	function copy(text) {
		if (navigator.clipboard) navigator.clipboard.writeText(text);
		flash('Copied to clipboard');
	}

	function flash(msg) {
		notice = msg;
		setTimeout(() => (notice = ''), 2500);
	}

	async function addSource() {
		error = '';
		try {
			await api.upsertSource(newSource.kind, newSource.location.trim());
			newSource.location = '';
			await load();
			flash('Source saved');
		} catch (e) {
			error = e.message;
		}
	}

	async function toggle(src) {
		error = '';
		try {
			await api.toggleSource(src.id, !src.enabled);
			await load();
		} catch (e) {
			error = e.message;
		}
	}

	async function saveSetting(key) {
		error = '';
		let value;
		try {
			value = JSON.parse(settings[key]);
		} catch {
			error = `invalid JSON for ${key}`;
			return;
		}
		try {
			await api.putSettings({ [key]: value });
			flash(`${key} saved`);
		} catch (e) {
			error = e.message;
		}
	}

	async function runAction(name) {
		error = '';
		busyAction = name;
		try {
			await api.action(name);
			flash(`Triggered ${name}`);
		} catch (e) {
			error = e.message;
		} finally {
			busyAction = '';
		}
	}

	const actions = [
		{ name: 'refresh-sources', label: 'Refresh sources' },
		{ name: 'retest', label: 'Retest' },
		{ name: 'publish', label: 'Publish' },
		{ name: 'refresh-geoip', label: 'Refresh GeoIP' }
	];

	onMount(load);
</script>

<h1 class="text-2xl font-bold mb-4">Admin</h1>

{#if error}
	<div class="alert alert-error mb-4"><span>{error}</span></div>
{/if}

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">Actions</h2>
		<p class="text-sm text-base-content/60">Out-of-band triggers handled by the coordinator's scheduler.</p>
		<div class="flex flex-wrap gap-2 mt-2">
			{#each actions as a}
				<button class="btn btn-outline btn-sm" onclick={() => runAction(a.name)} disabled={busyAction === a.name}>
					{#if busyAction === a.name}<span class="loading loading-spinner loading-xs"></span>{/if}
					{a.label}
				</button>
			{/each}
		</div>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">Workers</h2>
		<p class="text-sm text-base-content/60">
			Each worker authenticates with its own token. The name you choose is the worker's identity in
			the fleet. The secret is shown once — copy it into the worker's <span class="mono">WORKER_TOKEN</span>.
		</p>

		{#if freshToken}
			<div class="alert alert-success flex-col items-start gap-2 mt-2">
				<span class="font-medium">Token for "{freshToken.name}" created — copy it now, it won't be shown again:</span>
				<div class="join w-full">
					<input class="input input-bordered input-sm join-item w-full mono" readonly value={freshToken.token} />
					<button class="btn btn-sm join-item" onclick={() => copy(freshToken.token)}>Copy</button>
					<button class="btn btn-sm join-item" onclick={() => (freshToken = null)}>Dismiss</button>
				</div>
			</div>
		{/if}

		<div class="overflow-x-auto mt-2">
			<table class="table table-sm">
				<thead>
					<tr><th>Name</th><th>Created</th><th>Last used</th><th>Status</th><th></th></tr>
				</thead>
				<tbody>
					{#each tokens as tok}
						<tr class="hover">
							<td class="mono font-medium">{tok.name}</td>
							<td class="text-base-content/60">{ago(tok.created_at)}</td>
							<td class="text-base-content/60">{tok.last_used ? ago(tok.last_used) : 'never'}</td>
							<td>
								<span class="badge badge-sm {tok.enabled ? 'badge-success' : 'badge-ghost'}">
									{tok.enabled ? 'active' : 'disabled'}
								</span>
							</td>
							<td><button class="btn btn-xs btn-error btn-outline" onclick={() => revokeToken(tok)}>Revoke</button></td>
						</tr>
					{:else}
						<tr><td colspan="5" class="text-base-content/60 text-center py-4">No worker tokens yet.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>

		<form
			class="flex flex-wrap items-end gap-3 mt-4 pt-4 border-t border-base-300"
			onsubmit={(e) => {
				e.preventDefault();
				createToken();
			}}
		>
			<label class="form-control flex-1 min-w-60">
				<span class="label-text mb-1">Worker name</span>
				<input
					class="input input-bordered input-sm w-full"
					bind:value={newTokenName}
					placeholder="home-vps"
					pattern="[A-Za-z0-9-]+"
				/>
			</label>
			<button class="btn btn-primary btn-sm" type="submit">Create token</button>
		</form>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">Sources</h2>
		<div class="overflow-x-auto">
			<table class="table table-sm">
				<thead>
					<tr><th>Kind</th><th>Location</th><th>Last fetch</th><th>Enabled</th><th></th></tr>
				</thead>
				<tbody>
					{#each sources as src}
						<tr class="hover">
							<td><span class="badge badge-ghost badge-sm">{src.kind}</span></td>
							<td class="mono text-xs text-base-content/70 break-all max-w-md">{src.location}</td>
							<td class="text-base-content/60">{src.last_fetch ? ago(src.last_fetch) : '—'}</td>
							<td>
								<span class="badge badge-sm {src.enabled ? 'badge-success' : 'badge-ghost'}">
									{src.enabled ? 'yes' : 'no'}
								</span>
							</td>
							<td>
								<button class="btn btn-xs" onclick={() => toggle(src)}>
									{src.enabled ? 'Disable' : 'Enable'}
								</button>
							</td>
						</tr>
					{:else}
						<tr><td colspan="5" class="text-base-content/60 text-center py-4">No sources yet.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>

		<form
			class="flex flex-wrap items-end gap-3 mt-4 pt-4 border-t border-base-300"
			onsubmit={(e) => {
				e.preventDefault();
				addSource();
			}}
		>
			<label class="form-control">
				<span class="label-text mb-1">Kind</span>
				<select class="select select-bordered select-sm" bind:value={newSource.kind}>
					<option value="subscription_url">subscription_url</option>
					<option value="raw_file">raw_file</option>
				</select>
			</label>
			<label class="form-control flex-1 min-w-60">
				<span class="label-text mb-1">Location</span>
				<input
					class="input input-bordered input-sm w-full"
					bind:value={newSource.location}
					placeholder="https://… or /path/to/file"
				/>
			</label>
			<button class="btn btn-primary btn-sm" type="submit">Add / update</button>
		</form>
	</div>
</div>

<div class="card bg-base-100 shadow">
	<div class="card-body">
		<h2 class="card-title text-lg">Settings</h2>
		<div class="overflow-x-auto">
			<table class="table table-sm">
				<thead>
					<tr><th class="w-64">Key</th><th>Value (JSON)</th><th></th></tr>
				</thead>
				<tbody>
					{#each Object.keys(settings).sort() as key}
						<tr class="hover">
							<td class="mono text-sm">{key}</td>
							<td><input class="input input-bordered input-sm w-full mono" bind:value={settings[key]} /></td>
							<td><button class="btn btn-xs" onclick={() => saveSetting(key)}>Save</button></td>
						</tr>
					{:else}
						<tr><td colspan="3" class="text-base-content/60 text-center py-4">No settings.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
</div>

{#if notice}
	<div class="toast toast-end">
		<div class="alert alert-success">
			<span>{notice}</span>
		</div>
	</div>
{/if}
