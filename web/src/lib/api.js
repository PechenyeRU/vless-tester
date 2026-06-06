import { browser } from '$app/environment';

const TOKEN_KEY = 'admin_token';

export function getToken() {
	return browser ? localStorage.getItem(TOKEN_KEY) || '' : '';
}

export function setToken(t) {
	if (browser) localStorage.setItem(TOKEN_KEY, t);
}

// onUnauthorized registers a handler invoked when the API returns 401 (a stale
// or revoked token), so the app can drop the session and bounce to /login.
let unauthorizedHandler = null;
export function onUnauthorized(fn) {
	unauthorizedHandler = fn;
}

// buildServerQuery turns a filter object into a /servers query string. Pure and
// dependency-free so it is unit-testable without a browser. Empty/zero fields
// are omitted.
export function buildServerQuery(filter = {}) {
	const p = new URLSearchParams();
	if (filter.country) p.set('country', filter.country);
	if (filter.worker) p.set('worker', filter.worker);
	if (filter.minSpeed) p.set('min_speed', String(filter.minSpeed));
	if (filter.limit) p.set('limit', String(filter.limit));
	const s = p.toString();
	return s ? '?' + s : '';
}

async function req(method, path, body) {
	const headers = {};
	const token = getToken();
	if (token) headers['Authorization'] = 'Bearer ' + token;
	if (body !== undefined) headers['Content-Type'] = 'application/json';
	const res = await fetch('/api/v1' + path, {
		method,
		headers,
		body: body !== undefined ? JSON.stringify(body) : undefined
	});
	if (!res.ok) {
		if (res.status === 401 && unauthorizedHandler) unauthorizedHandler();
		let msg = res.statusText;
		try {
			const data = await res.json();
			if (data && data.error) msg = data.error;
		} catch {
			/* non-json body */
		}
		throw new Error(`${res.status}: ${msg}`);
	}
	const ct = res.headers.get('content-type') || '';
	return ct.includes('application/json') ? res.json() : null;
}

export const api = {
	servers: (filter) => req('GET', '/servers' + buildServerQuery(filter)),
	server: (id) => req('GET', '/servers/' + id),
	createServer: (body) => req('POST', '/servers', body),
	updateServer: (id, body) => req('PUT', '/servers/' + id, body),
	deleteServer: (id) => req('DELETE', '/servers/' + id),
	workers: () => req('GET', '/workers'),
	stats: () => req('GET', '/stats'),
	progress: () => req('GET', '/progress'),
	cancelCycle: () => req('POST', '/cancel-cycle'),
	logs: (since = 0) => req('GET', '/logs?since=' + since),
	sources: () => req('GET', '/sources'),
	upsertSource: (kind, location) => req('PUT', '/sources', { kind, location }),
	importSources: (text) => req('POST', '/sources/import', { text }),
	toggleSource: (id, enabled) => req('PUT', '/sources', { id, enabled }),
	settings: () => req('GET', '/settings'),
	putSettings: (patch) => req('PUT', '/settings', patch),
	setFunnel: (stages) => req('PUT', '/settings', { 'funnel.stages': stages }),
	notifyTest: () => req('POST', '/notify-test'),
	action: (name) => req('POST', '/actions/' + name),
	workerTokens: () => req('GET', '/worker-tokens'),
	createWorkerToken: (name, protocols) => req('POST', '/worker-tokens', { name, protocols }),
	setWorkerTokenProtocols: (id, protocols) => req('PUT', '/worker-tokens/' + id, { protocols }),
	revokeWorkerToken: (id) => req('DELETE', '/worker-tokens/' + id)
};
