<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { ago } from '$lib/format.js';

	// Protocol types the platform understands (matches model.Protocol).
	const PROTOCOLS = ['vless', 'vmess', 'trojan', 'ss', 'hysteria2', 'hysteria', 'tuic', 'anytls', 'socks'];
	// Media-unlock platforms the workers can probe (matches checks.KnownMediaPlatforms).
	const MEDIA = ['openai', 'gemini', 'claude', 'spotify', 'netflix', 'youtube', 'disney', 'tiktok'];

	let sources = $state([]);
	let settings = $state({}); // key -> string (raw JSON text, editable)
	let tokens = $state([]);
	let error = $state('');
	let notice = $state('');
	let newSource = $state({ kind: 'subscription_url', location: '' });
	let newTokenName = $state('');
	// Per-worker protocol selection for the new token (empty = all).
	let newTokenProtocols = $state(new Set());
	// The freshly minted secret, shown once after creation.
	let freshToken = $state(null); // { name, token }
	let busyAction = $state('');
	// Inline protocol editor state: token id -> Set of selected protocols.
	let editingToken = $state(null);
	let editProtocols = $state(new Set());
	// Global enabled-protocols set (disabling one excludes it from all checks).
	let globalProtocols = $state(new Set());
	// Media settings.
	let mediaEnabled = $state(false);
	let mediaTested = $state(new Set());
	let mediaRequire = $state(new Set());
	let ipRiskEnabled = $state(false);

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
			const enabled = (sett && sett['protocols.enabled']) || PROTOCOLS;
			globalProtocols = new Set(enabled);
			mediaEnabled = !!(sett && sett['media.enabled']);
			mediaTested = new Set((sett && sett['media.platforms']) || []);
			mediaRequire = new Set((sett && sett['media.require']) || []);
			ipRiskEnabled = !!(sett && sett['iprisk.enabled']);
		} catch (e) {
			error = e.message;
		}
	}

	async function saveMedia() {
		error = '';
		try {
			await api.putSettings({
				'media.enabled': mediaEnabled,
				'media.platforms': MEDIA.filter((p) => mediaTested.has(p)),
				'media.require': MEDIA.filter((p) => mediaRequire.has(p)),
				'iprisk.enabled': ipRiskEnabled
			});
			flash('Media settings saved');
		} catch (e) {
			error = e.message;
		}
	}

	// toggleIn returns a new Set with value toggled (Svelte 5 reactivity needs a
	// fresh reference).
	function toggleIn(set, value) {
		const next = new Set(set);
		next.has(value) ? next.delete(value) : next.add(value);
		return next;
	}

	// normalizeProtocols treats "all selected" or "none" as no restriction (empty).
	function normalizeProtocols(set) {
		if (set.size === 0 || set.size === PROTOCOLS.length) return [];
		return PROTOCOLS.filter((p) => set.has(p));
	}

	async function createToken() {
		error = '';
		const name = newTokenName.trim();
		if (!name) return;
		try {
			freshToken = await api.createWorkerToken(name, normalizeProtocols(newTokenProtocols));
			newTokenName = '';
			newTokenProtocols = new Set();
			await load();
		} catch (e) {
			error = e.message;
		}
	}

	function startEdit(tok) {
		editingToken = tok.id;
		editProtocols = new Set(tok.protocols && tok.protocols.length ? tok.protocols : PROTOCOLS);
	}

	async function saveEdit(tok) {
		error = '';
		try {
			await api.setWorkerTokenProtocols(tok.id, normalizeProtocols(editProtocols));
			editingToken = null;
			await load();
			flash(`Updated ${tok.name}`);
		} catch (e) {
			error = e.message;
		}
	}

	async function saveGlobalProtocols() {
		error = '';
		try {
			await api.putSettings({ 'protocols.enabled': PROTOCOLS.filter((p) => globalProtocols.has(p)) });
			flash('Enabled protocols saved');
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
					<tr><th>Name</th><th>Protocols</th><th>Created</th><th>Last used</th><th>Status</th><th></th></tr>
				</thead>
				<tbody>
					{#each tokens as tok}
						<tr class="hover">
							<td class="mono font-medium align-top">{tok.name}</td>
							<td>
								{#if editingToken === tok.id}
									<div class="flex flex-wrap gap-x-3 gap-y-1 max-w-md">
										{#each PROTOCOLS as p}
											<label class="label cursor-pointer gap-1 py-0">
												<input
													type="checkbox"
													class="checkbox checkbox-xs"
													checked={editProtocols.has(p)}
													onchange={() => (editProtocols = toggleIn(editProtocols, p))}
												/>
												<span class="text-xs mono">{p}</span>
											</label>
										{/each}
									</div>
								{:else if tok.protocols && tok.protocols.length}
									<div class="flex flex-wrap gap-1">
										{#each tok.protocols as p}<span class="badge badge-ghost badge-sm mono">{p}</span>{/each}
									</div>
								{:else}
									<span class="text-base-content/50 text-sm">all</span>
								{/if}
							</td>
							<td class="text-base-content/60 align-top">{ago(tok.created_at)}</td>
							<td class="text-base-content/60 align-top">{tok.last_used ? ago(tok.last_used) : 'never'}</td>
							<td class="align-top">
								<span class="badge badge-sm {tok.enabled ? 'badge-success' : 'badge-ghost'}">
									{tok.enabled ? 'active' : 'disabled'}
								</span>
							</td>
							<td class="align-top">
								<div class="flex gap-1">
									{#if editingToken === tok.id}
										<button class="btn btn-xs btn-primary" onclick={() => saveEdit(tok)}>Save</button>
										<button class="btn btn-xs" onclick={() => (editingToken = null)}>Cancel</button>
									{:else}
										<button class="btn btn-xs" onclick={() => startEdit(tok)}>Protocols</button>
										<button class="btn btn-xs btn-error btn-outline" onclick={() => revokeToken(tok)}>Revoke</button>
									{/if}
								</div>
							</td>
						</tr>
					{:else}
						<tr><td colspan="6" class="text-base-content/60 text-center py-4">No worker tokens yet.</td></tr>
					{/each}
				</tbody>
			</table>
		</div>

		<form
			class="mt-4 pt-4 border-t border-base-300"
			onsubmit={(e) => {
				e.preventDefault();
				createToken();
			}}
		>
			<div class="flex flex-wrap items-end gap-3">
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
			</div>
			<div class="mt-2">
				<span class="label-text">Allowed protocols <span class="text-base-content/50">(none selected = all)</span></span>
				<div class="flex flex-wrap gap-x-3 gap-y-1 mt-1">
					{#each PROTOCOLS as p}
						<label class="label cursor-pointer gap-1 py-0">
							<input
								type="checkbox"
								class="checkbox checkbox-xs"
								checked={newTokenProtocols.has(p)}
								onchange={() => (newTokenProtocols = toggleIn(newTokenProtocols, p))}
							/>
							<span class="text-xs mono">{p}</span>
						</label>
					{/each}
				</div>
			</div>
		</form>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">Protocols (global)</h2>
		<p class="text-sm text-base-content/60">
			Unchecking a protocol excludes it from every check, fleet-wide. Per-worker limits above are
			applied on top of this.
		</p>
		<div class="flex flex-wrap gap-x-4 gap-y-1 mt-2">
			{#each PROTOCOLS as p}
				<label class="label cursor-pointer gap-1 py-0">
					<input
						type="checkbox"
						class="checkbox checkbox-sm"
						checked={globalProtocols.has(p)}
						onchange={() => (globalProtocols = toggleIn(globalProtocols, p))}
					/>
					<span class="text-sm mono">{p}</span>
				</label>
			{/each}
		</div>
		<div class="mt-3">
			<button class="btn btn-primary btn-sm" onclick={saveGlobalProtocols}>Save protocols</button>
		</div>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">Media unlock</h2>
		<label class="label cursor-pointer justify-start gap-2 w-fit">
			<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={mediaEnabled} />
			<span class="label-text">Enable media-unlock checks</span>
		</label>

		<div class="mt-2">
			<span class="label-text">Tested platforms <span class="text-base-content/50">(probed and shown per node)</span></span>
			<div class="flex flex-wrap gap-x-4 gap-y-1 mt-1">
				{#each MEDIA as p}
					<label class="label cursor-pointer gap-1 py-0">
						<input
							type="checkbox"
							class="checkbox checkbox-sm"
							checked={mediaTested.has(p)}
							onchange={() => (mediaTested = toggleIn(mediaTested, p))}
						/>
						<span class="text-sm mono">{p}</span>
					</label>
				{/each}
			</div>
		</div>

		<div class="mt-3">
			<span class="label-text">
				Required to unlock <span class="text-base-content/50">(node must unlock all of these, or its speed test is skipped — saves time)</span>
			</span>
			<div class="flex flex-wrap gap-x-4 gap-y-1 mt-1">
				{#each MEDIA as p}
					<label class="label cursor-pointer gap-1 py-0">
						<input
							type="checkbox"
							class="checkbox checkbox-sm"
							checked={mediaRequire.has(p)}
							onchange={() => (mediaRequire = toggleIn(mediaRequire, p))}
						/>
						<span class="text-sm mono">{p}</span>
					</label>
				{/each}
			</div>
		</div>

		<div class="divider my-2"></div>

		<label class="label cursor-pointer justify-start gap-2 w-fit">
			<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={ipRiskEnabled} />
			<span class="label-text">Enable IP-risk scoring</span>
			<span class="text-base-content/50 text-sm">(tags each node's exit IP with a 0-100 risk score)</span>
		</label>

		<div class="mt-3">
			<button class="btn btn-primary btn-sm" onclick={saveMedia}>Save media settings</button>
		</div>
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
