import { browser } from '$app/environment';
import { getToken, setToken } from '$lib/api.js';

// auth holds the admin session. Users sign in with a username + password; the
// coordinator validates them and mints a session bearer token that api.js
// attaches to every request. The token is kept in localStorage so the session
// survives a reload. State is a rune so the layout guard reacts to login/logout.
let token = $state(browser ? getToken() : '');

export const auth = {
	get token() {
		return token;
	},
	get isAuthed() {
		return !!token;
	},
	// login exchanges credentials for the bearer token via POST /login.
	async login(username, password) {
		const res = await fetch('/api/v1/login', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username, password })
		});
		if (res.status === 401) throw new Error('Invalid username or password');
		if (!res.ok) {
			let msg = `${res.status}: ${res.statusText}`;
			try {
				const data = await res.json();
				if (data && data.error) msg = data.error;
			} catch {
				/* non-json body */
			}
			throw new Error(msg);
		}
		const data = await res.json();
		if (!data || !data.token) throw new Error('login did not return a token');
		setToken(data.token);
		token = data.token;
	},
	// logout clears the session locally first (so the UI reacts immediately), then
	// best-effort revokes it server-side. The revoke uses a direct fetch rather
	// than the api client so a 401 cannot re-trigger the onUnauthorized handler
	// that may have called logout in the first place.
	logout() {
		const current = token;
		setToken('');
		token = '';
		if (current) {
			fetch('/api/v1/logout', {
				method: 'POST',
				headers: { Authorization: 'Bearer ' + current }
			}).catch(() => {
				/* token already invalid or coordinator unreachable; ignore */
			});
		}
	}
};
