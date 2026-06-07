<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { ago } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

	// Protocol types the platform understands (matches model.Protocol).
	const PROTOCOLS = ['vless', 'vmess', 'trojan', 'ss', 'hysteria2', 'hysteria', 'tuic', 'anytls', 'socks'];
	// Media platforms the workers can probe (matches checks.KnownMediaPlatforms).
	const MEDIA = ['openai', 'gemini', 'claude', 'spotify', 'netflix', 'youtube', 'disney', 'tiktok'];

	// The funnel filters run after the connectivity check, in order. Each can be
	// enabled, reordered, and set to drop a node that does not pass it.
	const FUNNEL_DEFAULT = [
		{ check: 'media', gate: true },
		{ check: 'ip_risk', gate: false },
		{ check: 'speed', gate: false }
	];
	const FILTER_META = {
		media: { label: 'Media unlock', desc: 'Probe whether streaming and AI services work through the node.' },
		ip_risk: { label: 'IP risk', desc: "Score the exit IP reputation (proxy, datacenter, mobile)." },
		speed: { label: 'Speed', desc: 'Measure download and upload throughput (the expensive leg).' }
	};

	// Per-key explanations for the raw settings table (kept under Advanced).
	const SETTING_HELP = {
		'approval.max_latency_ms': 'Edited above in "Approval". Max latency (ms) a node may have to be published.',
		'approval.min_dl_mbps': 'Edited above in "Approval". Min download speed (MB/s) a node must reach.',
		'approval.required_workers': 'Edited above in "Approval". Distinct workers that must each confirm a node.',
		'approval.allow_partial': 'Edited above in "Approval". When the fleet is smaller than required, approve with as few as 1.',
		'speed.streams': 'Edited above in the Speed filter (parallel download streams).',
		'speed.bytes': 'Max bytes downloaded per speed test (adaptive may stop earlier).',
		'speed.adaptive': 'Edited above in the Speed filter (stop the speed test early once throughput is clear).',
		'dispatch.interval': 'How often a new test cycle is dispatched (e.g. 12h, 30m).',
		'reconcile.interval': 'How often dead-worker jobs are requeued and drained batches published.',
		'publish.interval': 'How often the working list is published.',
		'publish.github_repo': 'Separate GitHub repo the working list is pushed to.',
		'geoip.refresh': 'How often the GeoIP database is refreshed (about 2 weeks).',
		'jobs.lease_ttl': 'A claimed job older than this is considered dead and requeued.',
		'jobs.max_attempts': 'Max requeues before a job is marked failed.',
		'protocols.enabled': 'Fleet-wide protocol allow-list (empty = all). Per-worker protocols are usually enough; set this only to exclude a protocol everywhere at once.',
		'media.enabled': 'Edited above in the Media filter.',
		'media.platforms': 'Edited above in the Media filter (tested platforms).',
		'media.require': 'Edited above in the Media filter (required to unlock).',
		'iprisk.enabled': 'Edited above in the IP risk filter.',
		'dnsleak.enabled': 'Edited above in the DNS leak filter.',
		'iprisk.url': 'Edited above in the IP risk filter (provider URL; empty uses ip-api.com).',
		'sub.path': 'Obfuscated token for /sub: when set, the list is only served at /sub/<token> and bare /sub is hidden.',
		'dispatch.shuffle': 'Edited above in "Output filters" (randomize server order per cycle).',
		'dispatch.max_probes': 'Edited above in "Output filters" (cap servers tested per cycle; 0 = all).',
		'funnel.stages': 'Edited above in "Filters".',
		'notify.enabled': 'Edited above in "Notifications".',
		'notify.urls': 'Edited above in "Notifications" (shoutrrr URLs).',
		'speed.download_url': 'Edited above in the Speed filter (empty = Cloudflare).',
		'speed.upload_url': 'Edited above in the Speed filter (empty = Cloudflare).',
		'speed.download_mb': 'Edited above in the Speed filter (0 = use speed.bytes).',
		'speed.timeout_ms': 'Edited above in the Speed filter (per-node speed leg timeout).',
		'output.node_prefix': 'Edited above in "Output filters" (prepended to each node name).',
		'output.success_limit': 'Edited above in "Output filters" (0 = unlimited).',
		'filter.name_include': 'Edited above in "Output filters" (regex; keep only matching names).',
		'filter.name_exclude': 'Edited above in "Output filters" (regex; drop matching names).'
	};

	let sources = $state([]);
	let settings = $state({}); // key -> string (raw JSON text, editable)
	let tokens = $state([]);
	let error = $state('');
	let notice = $state('');
	// Bulk paste box for sources: one link, config or path per line.
	let newSourcesText = $state('');
	let importing = $state(false);
	let newTokenName = $state('');
	// Per-worker protocol selection for the new token (empty = all).
	let newTokenProtocols = $state(new Set());
	// The freshly minted secret, shown once after creation.
	let freshToken = $state(null); // { name, token }
	let busyAction = $state('');
	// Inline protocol editor state: token id -> Set of selected protocols.
	let editingToken = $state(null);
	let editProtocols = $state(new Set());
	// Fleet-wide protocol allow-list (protocols.enabled). null means no restriction
	// (all allowed); a non-empty set excludes everything outside it, so those
	// protocols are shown disabled in the per-worker pickers.
	let globalEnabled = $state(null);
	// Filter state.
	let mediaEnabled = $state(false);
	let mediaTested = $state(new Set());
	let mediaRequire = $state(new Set());
	let ipRiskEnabled = $state(false);
	let ipRiskUrl = $state('');
	let dnsLeakEnabled = $state(false);
	let speedEnabled = $state(true);
	// Funnel pipeline (ordered list of {check, gate}); always carries media,
	// ip_risk and speed so they can be reordered even while disabled.
	let funnelStages = $state([]);
	// Which filter's Advanced panel is open (one at a time keeps it tidy).
	let openFilter = $state('');
	let savingFilters = $state(false);
	// Approval (publish) thresholds.
	let approval = $state({ max_latency_ms: 800, min_dl_mbps: 1, required_workers: 1, allow_partial: true });
	// Notifications.
	let notifyEnabled = $state(false);
	let notifyUrls = $state(''); // one shoutrrr URL per line
	let notifyBusy = $state(false);
	// Speed test config.
	let speed = $state({ download_url: '', upload_url: '', streams: 6, download_mb: 0, timeout_ms: 30000, adaptive: true });
	// Output filters.
	let output = $state({ node_prefix: '', success_limit: 0, name_include: '', name_exclude: '' });
	// Dispatch knobs. max_probes optionally caps servers tested per cycle (0 = all).
	let dispatch = $state({ shuffle: false, max_probes: 0 });
	// Raw settings table (collapsed by default).
	let showRaw = $state(false);

	// normalizeFunnel guarantees media, ip_risk and speed are all present so each
	// has a row to reorder and gate, preserving any saved order.
	function normalizeFunnel(saved) {
		const order = Array.isArray(saved) && saved.length ? saved.map((s) => ({ ...s })) : FUNNEL_DEFAULT.map((s) => ({ ...s }));
		for (const def of FUNNEL_DEFAULT) {
			if (!order.some((s) => s.check === def.check)) order.push({ ...def });
		}
		return order;
	}

	async function load() {
		error = '';
		try {
			const [srcs, sett, toks] = await Promise.all([api.sources(), api.settings(), api.workerTokens()]);
			sources = srcs || [];
			tokens = toks || [];
			settings = Object.fromEntries(Object.entries(sett || {}).map(([k, v]) => [k, JSON.stringify(v)]));
			const ge = sett && sett['protocols.enabled'];
			globalEnabled = Array.isArray(ge) && ge.length ? new Set(ge) : null;
			mediaEnabled = !!(sett && sett['media.enabled']);
			mediaTested = new Set((sett && sett['media.platforms']) || []);
			mediaRequire = new Set((sett && sett['media.require']) || []);
			ipRiskEnabled = !!(sett && sett['iprisk.enabled']);
			ipRiskUrl = (sett && sett['iprisk.url']) || '';
			dnsLeakEnabled = !!(sett && sett['dnsleak.enabled']);
			const savedFunnel = sett && sett['funnel.stages'];
			speedEnabled = !Array.isArray(savedFunnel) || savedFunnel.some((s) => s.check === 'speed');
			funnelStages = normalizeFunnel(savedFunnel);
			approval = {
				max_latency_ms: (sett && sett['approval.max_latency_ms']) ?? 800,
				min_dl_mbps: (sett && sett['approval.min_dl_mbps']) ?? 1,
				required_workers: (sett && sett['approval.required_workers']) ?? 1,
				allow_partial: (sett && sett['approval.allow_partial']) ?? true
			};
			notifyEnabled = !!(sett && sett['notify.enabled']);
			notifyUrls = ((sett && sett['notify.urls']) || []).join('\n');
			output = {
				node_prefix: (sett && sett['output.node_prefix']) || '',
				success_limit: (sett && sett['output.success_limit']) ?? 0,
				name_include: (sett && sett['filter.name_include']) || '',
				name_exclude: (sett && sett['filter.name_exclude']) || ''
			};
			dispatch = {
				shuffle: !!(sett && sett['dispatch.shuffle']),
				max_probes: (sett && sett['dispatch.max_probes']) ?? 0
			};
			speed = {
				download_url: (sett && sett['speed.download_url']) || '',
				upload_url: (sett && sett['speed.upload_url']) || '',
				streams: (sett && sett['speed.streams']) ?? 6,
				download_mb: (sett && sett['speed.download_mb']) ?? 0,
				timeout_ms: (sett && sett['speed.timeout_ms']) ?? 30000,
				adaptive: !!(sett && sett['speed.adaptive'])
			};
		} catch (e) {
			error = e.message;
		}
	}

	// moveStage reorders a funnel filter up (-1) or down (+1).
	function moveStage(i, dir) {
		const j = i + dir;
		if (j < 0 || j >= funnelStages.length) return;
		const next = funnelStages.slice();
		[next[i], next[j]] = [next[j], next[i]];
		funnelStages = next;
	}

	function toggleGate(i) {
		const next = funnelStages.slice();
		next[i] = { ...next[i], gate: !next[i].gate };
		funnelStages = next;
	}

	function toggleAdvanced(key) {
		openFilter = openFilter === key ? '' : key;
	}

	function filterEnabled(check) {
		if (check === 'media') return mediaEnabled;
		if (check === 'ip_risk') return ipRiskEnabled;
		if (check === 'speed') return speedEnabled;
		return false;
	}

	function setFilterEnabled(check, on) {
		if (check === 'media') mediaEnabled = on;
		else if (check === 'ip_risk') ipRiskEnabled = on;
		else if (check === 'speed') speedEnabled = on;
	}

	// saveFilters persists the whole filter section: the funnel order and gates,
	// each filter's enabled flag, and the per-filter advanced options.
	async function saveFilters() {
		error = '';
		savingFilters = true;
		try {
			// Speed only ships as a stage when enabled; media and ip_risk always do
			// (a disabled one runs as a harmless no-op but keeps its place/gate).
			const stages = funnelStages.filter((s) => s.check !== 'speed' || speedEnabled);
			await api.putSettings({
				'funnel.stages': stages,
				'media.enabled': mediaEnabled,
				'media.platforms': MEDIA.filter((p) => mediaTested.has(p)),
				'media.require': MEDIA.filter((p) => mediaRequire.has(p)),
				'iprisk.enabled': ipRiskEnabled,
				'iprisk.url': ipRiskUrl.trim(),
				'dnsleak.enabled': dnsLeakEnabled,
				'speed.download_url': speed.download_url.trim(),
				'speed.upload_url': speed.upload_url.trim(),
				'speed.streams': Number(speed.streams),
				'speed.download_mb': Number(speed.download_mb),
				'speed.timeout_ms': Number(speed.timeout_ms),
				'speed.adaptive': speed.adaptive
			});
			flash('Filters saved');
		} catch (e) {
			error = e.message;
		} finally {
			savingFilters = false;
		}
	}

	async function saveApproval() {
		error = '';
		try {
			await api.putSettings({
				'approval.max_latency_ms': Number(approval.max_latency_ms),
				'approval.min_dl_mbps': Number(approval.min_dl_mbps),
				'approval.required_workers': Number(approval.required_workers),
				'approval.allow_partial': approval.allow_partial
			});
			flash('Approval saved');
		} catch (e) {
			error = e.message;
		}
	}

	async function saveNotify() {
		error = '';
		const urls = notifyUrls
			.split('\n')
			.map((u) => u.trim())
			.filter(Boolean);
		try {
			await api.putSettings({ 'notify.enabled': notifyEnabled, 'notify.urls': urls });
			flash('Notifications saved');
		} catch (e) {
			error = e.message;
		}
	}

	async function saveOutput() {
		error = '';
		try {
			await api.putSettings({
				'output.node_prefix': output.node_prefix,
				'output.success_limit': Number(output.success_limit),
				'filter.name_include': output.name_include.trim(),
				'filter.name_exclude': output.name_exclude.trim()
			});
			flash('Output filters saved');
		} catch (e) {
			error = e.message;
		}
	}

	async function saveDispatch() {
		error = '';
		try {
			await api.putSettings({
				'dispatch.shuffle': dispatch.shuffle,
				'dispatch.max_probes': Number(dispatch.max_probes)
			});
			flash('Dispatch settings saved');
		} catch (e) {
			error = e.message;
		}
	}

	async function testNotify() {
		error = '';
		notifyBusy = true;
		try {
			await api.notifyTest();
			flash('Test notification sent');
		} catch (e) {
			error = e.message;
		} finally {
			notifyBusy = false;
		}
	}

	// toggleIn returns a new Set with value toggled (Svelte 5 reactivity needs a
	// fresh reference).
	function toggleIn(set, value) {
		const next = new Set(set);
		next.has(value) ? next.delete(value) : next.add(value);
		return next;
	}

	// protoExcluded reports whether a protocol is turned off fleet-wide
	// (protocols.enabled is set and does not list it), so the per-worker pickers
	// show it disabled.
	function protoExcluded(p) {
		return globalEnabled !== null && !globalEnabled.has(p);
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

	async function importSources() {
		error = '';
		const text = newSourcesText.trim();
		if (!text) return;
		importing = true;
		try {
			const r = await api.importSources(text);
			newSourcesText = '';
			await load();
			const parts = [];
			if (r.subscription) parts.push(`${r.subscription} subscription`);
			if (r.inline) parts.push(`${r.inline} inline`);
			if (r.file) parts.push(`${r.file} file`);
			let msg = `Added ${r.added} source${r.added === 1 ? '' : 's'}`;
			if (parts.length) msg += ` (${parts.join(', ')})`;
			if (r.skipped && r.skipped.length) msg += `, skipped ${r.skipped.length}`;
			flash(msg);
		} catch (e) {
			error = e.message;
		} finally {
			importing = false;
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

	// sourceKindLabel gives a friendly name for a source kind.
	function sourceKindLabel(kind) {
		if (kind === 'subscription_url') return 'subscription';
		if (kind === 'raw_inline') return 'inline config';
		if (kind === 'raw_file') return 'file';
		return kind;
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
		<h2 class="card-title text-lg">
			Actions
			<Help tip="Trigger a coordinator job now instead of waiting for the schedule. Refresh sources re-ingests and dispatches a cycle; Publish re-evaluates the approval gate against history and pushes (no retest)." />
		</h2>
		<p class="text-sm text-base-content/60">Out-of-band triggers handled by the coordinator scheduler.</p>
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
			Each worker authenticates with its own token. The name you choose is the worker identity in the
			fleet. The secret is shown once, so copy it into the worker's <span class="mono">WORKER_TOKEN</span>.
		</p>

		{#if freshToken}
			<div class="alert alert-success flex-col items-start gap-2 mt-2">
				<span class="font-medium">Token for "{freshToken.name}" created. Copy it now, it will not be shown again:</span>
				<div class="join w-full">
					<input
						class="input input-bordered input-sm join-item w-full mono bg-base-100 text-base-content"
						readonly
						value={freshToken.token}
					/>
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
											<label
												class="label gap-1 py-0 {protoExcluded(p) ? 'tooltip cursor-not-allowed opacity-40' : 'cursor-pointer'}"
												data-tip={protoExcluded(p) ? 'Excluded fleet-wide by the global protocol setting' : null}
											>
												<input
													type="checkbox"
													class="checkbox checkbox-xs"
													checked={editProtocols.has(p)}
													disabled={protoExcluded(p)}
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
			<details class="mt-2">
				<summary class="cursor-pointer text-sm text-base-content/70 select-none">Advanced: allowed protocols</summary>
				<div class="mt-2 pl-1">
					<span class="label-text">Allowed protocols <span class="text-base-content/50">(none selected = all)</span></span>
					<div class="flex flex-wrap gap-x-3 gap-y-1 mt-1">
						{#each PROTOCOLS as p}
							<label
								class="label gap-1 py-0 {protoExcluded(p) ? 'tooltip cursor-not-allowed opacity-40' : 'cursor-pointer'}"
								data-tip={protoExcluded(p) ? 'Excluded fleet-wide by the global protocol setting' : null}
							>
								<input
									type="checkbox"
									class="checkbox checkbox-xs"
									checked={newTokenProtocols.has(p)}
									disabled={protoExcluded(p)}
									onchange={() => (newTokenProtocols = toggleIn(newTokenProtocols, p))}
								/>
								<span class="text-xs mono">{p}</span>
							</label>
						{/each}
					</div>
				</div>
			</details>
		</form>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">
			Sources
			<Help tip="Subscription URLs, raw share links or local file paths the coordinator fetches, parses and tests every cycle. Disabled sources are skipped." />
		</h2>
		<p class="text-sm text-base-content/60">
			Paste anything, one entry per line. Each line is classified automatically: a share link
			(vless, vmess, trojan, ss, hysteria2, tuic and so on) is stored as an inline config, an http or
			https link as a subscription, and anything else as a local file path.
		</p>

		<form
			class="mt-2"
			onsubmit={(e) => {
				e.preventDefault();
				importSources();
			}}
		>
			<textarea
				class="textarea textarea-bordered mono text-xs w-full h-28"
				bind:value={newSourcesText}
				placeholder={'https://example.com/sub\nvless://uuid@host:443?...#node\n/path/to/links.txt'}
			></textarea>
			<div class="mt-2">
				<button class="btn btn-primary btn-sm" type="submit" disabled={importing}>
					{#if importing}<span class="loading loading-spinner loading-xs"></span>{/if}
					Add sources
				</button>
			</div>
		</form>

		<div class="overflow-x-auto mt-4">
			<table class="table table-sm">
				<thead>
					<tr><th>Kind</th><th>Location</th><th>Last fetch</th><th>Enabled</th><th></th></tr>
				</thead>
				<tbody>
					{#each sources as src}
						<tr class="hover">
							<td><span class="badge badge-ghost badge-sm">{sourceKindLabel(src.kind)}</span></td>
							<td class="mono text-xs text-base-content/70 break-all max-w-md">{src.location}</td>
							<td class="text-base-content/60">{src.last_fetch ? ago(src.last_fetch) : 'never'}</td>
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
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">
			Filters
			<Help tip="The checks each node runs after connectivity, in order. Enable a filter, open its Advanced panel for options, and turn on 'Drop if it fails' to discard nodes that do not pass (which also skips the rest of the funnel for that node)." />
		</h2>
		<p class="text-sm text-base-content/60">
			Connectivity always runs first: a node that cannot connect is dropped. The filters below run in
			the order shown. Turn on "Drop if it fails" to discard a node that does not pass.
		</p>

		<ul class="mt-3 flex flex-col gap-2">
			<li class="flex items-center gap-3 rounded-lg bg-base-200 px-3 py-2 opacity-70">
				<span class="badge badge-ghost badge-sm w-6 justify-center">1</span>
				<div class="flex-1">
					<span class="font-medium">Connectivity</span>
					<span class="text-xs text-base-content/50 ml-2 hidden sm:inline">Always runs first; a node that cannot connect is dropped.</span>
				</div>
				<span class="badge badge-sm badge-warning">always on</span>
			</li>

			{#each funnelStages as st, i (st.check)}
				<li class="rounded-lg bg-base-200 px-3 py-2">
					<div class="flex items-center gap-3 flex-wrap">
						<span class="badge badge-ghost badge-sm w-6 justify-center">{i + 2}</span>
						<label class="cursor-pointer">
							<input
								type="checkbox"
								class="toggle toggle-sm toggle-primary align-middle"
								checked={filterEnabled(st.check)}
								onchange={(e) => setFilterEnabled(st.check, e.currentTarget.checked)}
							/>
						</label>
						<div class="flex-1 min-w-40">
							<span class={filterEnabled(st.check) ? 'font-medium' : 'font-medium opacity-50'}>
								{FILTER_META[st.check]?.label || st.check}
							</span>
							<span class="text-xs text-base-content/50 ml-2 hidden sm:inline">{FILTER_META[st.check]?.desc || ''}</span>
						</div>
						<label class="label cursor-pointer gap-2 py-0" class:opacity-40={!filterEnabled(st.check)}>
							<span class="label-text text-sm">Drop if it fails</span>
							<input type="checkbox" class="toggle toggle-xs toggle-primary" checked={st.gate} onchange={() => toggleGate(i)} disabled={!filterEnabled(st.check)} />
						</label>
						<div class="join">
							<button class="btn btn-xs join-item" onclick={() => moveStage(i, -1)} disabled={i === 0} aria-label="move up" title="Move up">
								<svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 10l4-4 4 4" stroke-linecap="round" stroke-linejoin="round" /></svg>
							</button>
							<button class="btn btn-xs join-item" onclick={() => moveStage(i, 1)} disabled={i === funnelStages.length - 1} aria-label="move down" title="Move down">
								<svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round" /></svg>
							</button>
						</div>
						<button class="btn btn-xs btn-ghost" onclick={() => toggleAdvanced(st.check)}>
							{openFilter === st.check ? 'Hide' : 'Advanced'}
						</button>
					</div>

					{#if openFilter === st.check}
						<div class="mt-3 pt-3 border-t border-base-300 pl-1">
							{#if st.check === 'media'}
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
								<div class="mt-3">
									<span class="label-text">
										Required to unlock <span class="text-base-content/50">(node must unlock all of these, otherwise its speed test is skipped to save time)</span>
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
							{:else if st.check === 'ip_risk'}
								<label class="form-control">
									<span class="label-text mb-1">Provider URL <span class="text-base-content/50">(empty uses the default ip-api.com)</span></span>
									<input class="input input-bordered input-sm mono text-xs" bind:value={ipRiskUrl} placeholder="https://ip-api.com/json" />
								</label>
							{:else if st.check === 'speed'}
								<div class="grid gap-3 sm:grid-cols-2">
									<label class="form-control">
										<span class="label-text mb-1">Download URL <span class="text-base-content/50">(__down-style; empty = Cloudflare)</span></span>
										<input class="input input-bordered input-sm mono text-xs" bind:value={speed.download_url} placeholder="https://speed.example.com/__down" />
									</label>
									<label class="form-control">
										<span class="label-text mb-1">Upload URL <span class="text-base-content/50">(__up-style; empty disables upload)</span></span>
										<input class="input input-bordered input-sm mono text-xs" bind:value={speed.upload_url} placeholder="https://speed.example.com/__up" />
									</label>
									<label class="form-control">
										<span class="label-text mb-1">Streams</span>
										<input type="number" min="1" class="input input-bordered input-sm w-28" bind:value={speed.streams} />
									</label>
									<label class="form-control">
										<span class="label-text mb-1">Download MB <span class="text-base-content/50">(0 = default)</span></span>
										<input type="number" min="0" class="input input-bordered input-sm w-28" bind:value={speed.download_mb} />
									</label>
									<label class="form-control">
										<span class="label-text mb-1">Timeout (ms)</span>
										<input type="number" min="1000" step="1000" class="input input-bordered input-sm w-28" bind:value={speed.timeout_ms} />
									</label>
									<label class="label cursor-pointer justify-start gap-2 self-end">
										<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={speed.adaptive} />
										<span class="label-text">Adaptive (stop early)</span>
									</label>
								</div>
							{/if}
						</div>
					{/if}
				</li>
			{/each}

			<li class="rounded-lg bg-base-200 px-3 py-2">
				<div class="flex items-center gap-3 flex-wrap">
					<span class="badge badge-ghost badge-sm w-6 justify-center">+</span>
					<label class="cursor-pointer">
						<input type="checkbox" class="toggle toggle-sm toggle-primary align-middle" bind:checked={dnsLeakEnabled} />
					</label>
					<div class="flex-1 min-w-40">
						<span class={dnsLeakEnabled ? 'font-medium' : 'font-medium opacity-50'}>DNS leak</span>
						<span class="text-xs text-base-content/50 ml-2 hidden sm:inline">Flags nodes whose DNS resolver country differs from the exit (informational, never drops).</span>
					</div>
					<span class="badge badge-sm badge-ghost">informational</span>
				</div>
			</li>
		</ul>

		<div class="mt-3">
			<button class="btn btn-primary btn-sm" onclick={saveFilters} disabled={savingFilters}>
				{#if savingFilters}<span class="loading loading-spinner loading-xs"></span>{/if}
				Save filters
			</button>
		</div>

		<div class="divider my-2">Approval</div>
		<p class="text-sm text-base-content/60">
			The thresholds a measured node must clear to be published, and how many distinct workers must
			agree.
		</p>
		<div class="grid gap-3 sm:grid-cols-2 mt-2">
			<label class="form-control">
				<span class="label-text mb-1">Max latency (ms)</span>
				<input type="number" min="1" class="input input-bordered input-sm w-32" bind:value={approval.max_latency_ms} />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Min download (MB/s)</span>
				<input type="number" min="0" step="0.1" class="input input-bordered input-sm w-32" bind:value={approval.min_dl_mbps} />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">
					Min confirming workers
					<Help tip="Distinct workers that must each measure a node within the thresholds before it is published." />
				</span>
				<input type="number" min="1" class="input input-bordered input-sm w-32" bind:value={approval.required_workers} />
			</label>
			<label class="label cursor-pointer justify-start gap-2 self-end">
				<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={approval.allow_partial} />
				<span class="label-text">
					Bypass the minimum on a small fleet
					<Help tip="When fewer workers are alive than the minimum, publish with as few as one confirmation instead of holding the node back." />
				</span>
			</label>
		</div>
		<div class="mt-3">
			<button class="btn btn-primary btn-sm" onclick={saveApproval}>Save approval</button>
		</div>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">
			Output filters
			<Help tip="Shape the published list at publish time (no retest). Regex match the full node name (flag, brand, seq, speed, tags). Applied to every output format." />
		</h2>
		<div class="grid gap-3 sm:grid-cols-2">
			<label class="form-control">
				<span class="label-text mb-1">Keep names matching <span class="text-base-content/50">(regex; empty = all)</span></span>
				<input class="input input-bordered input-sm mono text-xs" bind:value={output.name_include} placeholder="FR|DE" />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Drop names matching <span class="text-base-content/50">(regex)</span></span>
				<input class="input input-bordered input-sm mono text-xs" bind:value={output.name_exclude} placeholder="OT" />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Node prefix <span class="text-base-content/50">(prepended to each name)</span></span>
				<input class="input input-bordered input-sm mono text-xs" bind:value={output.node_prefix} />
			</label>
			<label class="form-control">
				<span class="label-text mb-1">Success limit <span class="text-base-content/50">(0 = unlimited)</span></span>
				<input type="number" min="0" class="input input-bordered input-sm w-28" bind:value={output.success_limit} />
			</label>
		</div>
		<div class="mt-3">
			<button class="btn btn-primary btn-sm" onclick={saveOutput}>Save output filters</button>
		</div>

		<div class="divider my-2"></div>
		<details class="rounded border border-base-300 p-2">
			<summary class="cursor-pointer text-sm font-medium">Advanced dispatch</summary>
			<div class="mt-3 flex flex-wrap items-end gap-4">
				<label class="label cursor-pointer gap-2">
					<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={dispatch.shuffle} />
					<span class="label-text">Shuffle test order</span>
					<Help tip="Randomize the server order each cycle, so with a cap a large list is sampled across runs instead of always testing the same prefix." />
				</label>
				<label class="form-control">
					<span class="label-text mb-1">Max probes / run <span class="text-base-content/50">(0 = all; capacity-aware claiming bounds the rest)</span></span>
					<input type="number" min="0" class="input input-bordered input-sm w-28" bind:value={dispatch.max_probes} />
				</label>
				<button class="btn btn-primary btn-sm" onclick={saveDispatch}>Save dispatch</button>
			</div>
		</details>
	</div>
</div>

<div class="card bg-base-100 shadow mb-6">
	<div class="card-body">
		<h2 class="card-title text-lg">
			Notifications
			<Help
				tip="Send a per-country summary after each published cycle via shoutrrr. One service URL per line: telegram://token@telegram?chats=@channel, discord://token@id, slack://, or generic://host/path for a webhook. Applied on the next cycle; use 'Send test' to validate now."
			/>
		</h2>
		<label class="label cursor-pointer justify-start gap-2 w-fit">
			<input type="checkbox" class="toggle toggle-sm toggle-primary" bind:checked={notifyEnabled} />
			<span class="label-text">Notify on end of cycle</span>
		</label>
		<label class="form-control mt-2">
			<span class="label-text mb-1">
				Service URLs <span class="text-base-content/50">(one shoutrrr URL per line)</span>
			</span>
			<textarea
				class="textarea textarea-bordered mono text-xs h-28"
				bind:value={notifyUrls}
				placeholder={'telegram://token@telegram?chats=@mychannel\ndiscord://token@id\ngeneric://example.com/webhook'}
			></textarea>
		</label>
		<div class="mt-3 flex gap-2">
			<button class="btn btn-primary btn-sm" onclick={saveNotify}>Save notifications</button>
			<button class="btn btn-sm" onclick={testNotify} disabled={notifyBusy}>
				{#if notifyBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
				Send test
			</button>
		</div>
	</div>
</div>

<div class="card bg-base-100 shadow">
	<div class="card-body">
		<h2 class="card-title text-lg">
			Advanced
			<Help tip="Raw key/value config (JSON). Most keys have a typed editor in the cards above; hover the info icon on a key for what it does." />
		</h2>
		<button class="btn btn-sm btn-ghost w-fit" onclick={() => (showRaw = !showRaw)}>
			{showRaw ? 'Hide raw settings' : 'Show raw settings'}
		</button>
		{#if showRaw}
			<div class="overflow-x-auto mt-2">
				<table class="table table-sm">
					<thead>
						<tr><th class="w-64">Key</th><th>Value (JSON)</th><th></th></tr>
					</thead>
					<tbody>
						{#each Object.keys(settings).sort() as key}
							<tr class="hover">
								<td class="mono text-sm">
									{key}{#if SETTING_HELP[key]}<Help tip={SETTING_HELP[key]} pos="right" />{/if}
								</td>
								<td><input class="input input-bordered input-sm w-full mono" bind:value={settings[key]} /></td>
								<td><button class="btn btn-xs" onclick={() => saveSetting(key)}>Save</button></td>
							</tr>
						{:else}
							<tr><td colspan="3" class="text-base-content/60 text-center py-4">No settings.</td></tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
</div>

{#if notice}
	<div class="toast toast-end">
		<div class="alert alert-success">
			<span>{notice}</span>
		</div>
	</div>
{/if}
