<script>
	import { onMount } from 'svelte';
	import { api } from '$lib/api.js';
	import { ago } from '$lib/format.js';

	let sources = $state([]);
	let settings = $state({}); // key -> string (raw JSON text, editable)
	let error = $state('');
	let notice = $state('');
	let newSource = $state({ kind: 'subscription_url', location: '' });

	async function load() {
		error = '';
		try {
			const [srcs, sett] = await Promise.all([api.sources(), api.settings()]);
			sources = srcs || [];
			// settings come as {key: rawJSON}; render each as compact text for editing.
			settings = Object.fromEntries(
				Object.entries(sett || {}).map(([k, v]) => [k, JSON.stringify(v)])
			);
		} catch (e) {
			error = e.message;
		}
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
			flash('source saved');
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
		try {
			await api.action(name);
			flash(`triggered ${name}`);
		} catch (e) {
			error = e.message;
		}
	}

	const actions = ['refresh-sources', 'retest', 'publish', 'refresh-geoip'];

	onMount(load);
</script>

<h1>Admin</h1>

{#if error}<p class="error">{error}</p>{/if}
{#if notice}<p class="ok">{notice}</p>{/if}

<h2>Actions</h2>
<div class="row">
	{#each actions as a}
		<button onclick={() => runAction(a)}>{a}</button>
	{/each}
</div>

<h2>Sources</h2>
<table>
	<thead>
		<tr><th>kind</th><th>location</th><th>last fetch</th><th>enabled</th><th></th></tr>
	</thead>
	<tbody>
		{#each sources as src}
			<tr>
				<td>{src.kind}</td>
				<td class="muted" style="word-break:break-all">{src.location}</td>
				<td class="muted">{src.last_fetch ? ago(src.last_fetch) : '—'}</td>
				<td class:ok={src.enabled} class:bad={!src.enabled}>{src.enabled ? 'yes' : 'no'}</td>
				<td><button onclick={() => toggle(src)}>{src.enabled ? 'disable' : 'enable'}</button></td>
			</tr>
		{/each}
	</tbody>
</table>

<form class="row" onsubmit={(e) => { e.preventDefault(); addSource(); }}>
	<label class="field">
		kind
		<select bind:value={newSource.kind}>
			<option value="subscription_url">subscription_url</option>
			<option value="raw_file">raw_file</option>
		</select>
	</label>
	<label class="field" style="flex:1">
		location
		<input bind:value={newSource.location} placeholder="https://… or /path/to/file" />
	</label>
	<button class="primary" type="submit">Add / update</button>
</form>

<h2>Settings</h2>
<table>
	<thead>
		<tr><th>key</th><th>value (JSON)</th><th></th></tr>
	</thead>
	<tbody>
		{#each Object.keys(settings).sort() as key}
			<tr>
				<td>{key}</td>
				<td><input bind:value={settings[key]} style="width:100%" /></td>
				<td><button onclick={() => saveSetting(key)}>save</button></td>
			</tr>
		{/each}
	</tbody>
</table>
