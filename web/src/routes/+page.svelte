<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { flag, mbps, ago, dur } from '$lib/format.js';
	import Help from '$lib/Help.svelte';

	let stats = $state(null);
	let workers = $state([]);
	let error = $state('');
	let loading = $state(true);

	// Live cycle progress + log tail (polled).
	let progress = $state(null);
	let logLines = $state([]);
	let logSeq = $state(0);
	let autoScroll = $state(true);
	let logEl = $state();

	async function pollLive() {
		try {
			progress = await api.progress();
			const r = await api.logs(logSeq);
			if (r && r.lines && r.lines.length) {
				logLines = [...logLines, ...r.lines].slice(-300);
				logSeq = r.next_seq;
			}
		} catch {
			/* transient; keep last good state */
		}
	}

	// Keep the log view pinned to the newest line unless the user scrolled up.
	$effect(() => {
		logLines;
		if (autoScroll && logEl) logEl.scrollTop = logEl.scrollHeight;
	});

	function onLogScroll() {
		if (!logEl) return;
		autoScroll = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 24;
	}

	let cancelling = $state(false);
	async function cancelCycle() {
		if (!confirm('Cancel the running cycle? Open jobs are dropped and the batch is closed without publishing.')) return;
		cancelling = true;
		try {
			await api.cancelCycle();
			await pollLive();
		} catch (e) {
			error = e.message;
		} finally {
			cancelling = false;
		}
	}

	// Obfuscated /sub token (sub.path): when set, the working list is only served
	// at /sub/<token>, so the copied links must carry it.
	let subPath = $state('');

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
		try {
			const sett = await api.settings();
			subPath = (sett && sett['sub.path']) || '';
		} catch {
			/* settings are best-effort here; links just fall back to bare /sub */
		}
	}

	const aliveCount = $derived(workers.filter((w) => w.status !== 'dead').length);

	// Public subscription distribution links (served unauthenticated by /sub).
	const subTargets = [
		{ id: 'base64', label: 'Base64', desc: 'Universal base64 list for v2rayN, Nekobox, most clients.' },
		{ id: 'singbox', label: 'sing-box', desc: 'sing-box JSON (outbounds + selector/urltest).' },
		{ id: 'clash', label: 'Clash / Mihomo', desc: 'Full mihomo config with proxy-groups + ACL4SSR rules.' },
		{ id: 'v2ray', label: 'V2Ray', desc: 'Plain newline list of share URIs.' },
		{ id: 'surge', label: 'Surge', desc: 'Surge proxy list (supported protocols only).' }
	];
	let copied = $state('');

	function subUrl(target) {
		const origin = typeof window !== 'undefined' ? window.location.origin : '';
		const base = subPath ? `${origin}/sub/${encodeURIComponent(subPath)}` : `${origin}/sub`;
		return `${base}?target=${target}`;
	}

	async function copy(target) {
		try {
			await navigator.clipboard.writeText(subUrl(target));
			copied = target;
			setTimeout(() => {
				if (copied === target) copied = '';
			}, 1500);
		} catch (e) {
			error = e.message;
		}
	}

	onMount(() => {
		load();
		pollLive();
		const id = setInterval(pollLive, 2000);
		return () => clearInterval(id);
	});
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
			<div class="stat-title">
				Servers <Help tip="Unique deduplicated proxy endpoints ingested from all sources." pos="bottom" />
			</div>
			<div class="stat-value text-primary">{stats.servers}</div>
		</div>
		<div class="stat">
			<div class="stat-title">
				Test runs <Help tip="Total measurements recorded across all workers and cycles." pos="bottom" />
			</div>
			<div class="stat-value">{stats.runs}</div>
		</div>
		<div class="stat">
			<div class="stat-title">
				Workers <Help tip="Probes in the fleet. Alive = seen within the heartbeat window; total includes recently dead ones." pos="bottom" />
			</div>
			<div class="stat-value">
				{aliveCount}<span class="text-base font-normal text-base-content/50">/{workers.length}</span>
			</div>
			<div class="stat-desc">alive / total</div>
		</div>
		<div class="stat">
			<div class="stat-title">
				Countries <Help tip="Distinct countries among tested nodes (GeoIP on the exit IP)." pos="bottom" />
			</div>
			<div class="stat-value">{(stats.by_country || []).length}</div>
		</div>
	</div>

	<div class="card bg-base-100 shadow mb-6">
		<div class="card-body">
			<div class="flex items-center justify-between">
				<h2 class="text-lg font-semibold">
					Test cycle
					<Help tip="Progress of the in-flight test cycle (the current batch of jobs across the fleet). ETA is extrapolated from the completion rate so far." />
				</h2>
				{#if progress?.active}
					<button class="btn btn-xs btn-error btn-outline" onclick={cancelCycle} disabled={cancelling}>
						{#if cancelling}<span class="loading loading-spinner loading-xs"></span>{/if}
						Cancel cycle
					</button>
				{/if}
			</div>
			{#if progress?.active}
				<div class="flex flex-wrap items-center justify-between gap-x-3 text-sm mb-1">
					<span>{progress.completed} / {progress.total} jobs · {Math.round(progress.percent)}%</span>
					<span class="text-base-content/60">
						ETA {dur(progress.eta_seconds)} · {progress.per_minute
							? Math.round(progress.per_minute) + '/min'
							: '-'} · elapsed {dur(progress.elapsed_seconds)}
					</span>
				</div>
				<progress class="progress progress-primary w-full" value={progress.percent} max="100"></progress>
				<div class="flex gap-2 mt-2 text-xs">
					<span class="badge badge-success badge-sm">done {progress.done}</span>
					<span class="badge badge-error badge-sm">failed {progress.failed}</span>
					<span class="badge badge-ghost badge-sm">open {progress.open}</span>
				</div>
			{:else}
				<p class="text-sm text-base-content/60">
					Idle, no test cycle running. Trigger one from the
					<a class="link" href="/admin">Admin page (Actions)</a>.
				</p>
			{/if}

			<div class="mt-4">
				<div class="flex items-center justify-between mb-1">
					<span class="label-text text-sm">
						Live log
						<Help tip="Recent coordinator log lines, polled every 2s. Scroll up to pause auto-follow." />
					</span>
					<span class="text-xs text-base-content/50">{autoScroll ? 'following' : 'paused'}</span>
				</div>
				<div
					bind:this={logEl}
					onscroll={onLogScroll}
					class="mono text-xs bg-base-300/50 rounded-lg p-3 h-56 overflow-y-auto whitespace-pre-wrap"
				>
					{#each logLines as l (l.seq)}
						<div class="text-base-content/80">{l.msg}</div>
					{:else}
						<div class="text-base-content/40">No log lines yet.</div>
					{/each}
				</div>
			</div>
		</div>
	</div>

	<div class="card bg-base-100 shadow mb-6">
		<div class="card-body">
			<h2 class="text-lg font-semibold">
				Subscriptions
				<Help tip="Public, unauthenticated distribution links (GET /sub?target=…). Anyone with a link can fetch the working list, so treat them as semi-secret. They update on each publish." />
			</h2>
			<p class="text-sm text-base-content/60">
				Public distribution links for the latest working list. Point your client at one of these.
			</p>
			<div class="grid gap-3 sm:grid-cols-2 mt-2">
				{#each subTargets as t}
					<div>
						<div class="join w-full">
							<span class="join-item btn btn-sm btn-disabled w-32 justify-start">{t.label}</span>
							<input
								class="join-item input input-sm input-bordered flex-1 mono text-xs"
								readonly
								value={subUrl(t.id)}
							/>
							<a
								class="join-item btn btn-sm"
								href={subUrl(t.id)}
								target="_blank"
								rel="noreferrer"
								aria-label="open {t.label} subscription">Open</a
							>
							<button class="join-item btn btn-sm btn-primary" onclick={() => copy(t.id)}>
								{copied === t.id ? 'Copied' : 'Copy'}
							</button>
						</div>
						<p class="text-xs text-base-content/50 mt-0.5 ml-1">{t.desc}</p>
					</div>
				{/each}
			</div>
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
									<td>{w.capacity?.latency ?? '-'}</td>
									<td>{w.capacity?.speed ?? '-'}</td>
									<td>{w.capacity?.bw_mbps ? w.capacity.bw_mbps.toFixed(1) + ' MB/s' : '-'}</td>
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
									<td>{flag(c.country)} {c.country || '-'}</td>
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
