// Small pure formatters shared across views.

const FLAG_BASE = 0x1f1e6;

// flag turns an ISO-3166 alpha-2 country code into its emoji flag. Unknown or
// empty codes render a neutral globe.
export function flag(country) {
	if (!country || country.length !== 2) return '🌐';
	const cc = country.toUpperCase();
	if (!/^[A-Z]{2}$/.test(cc)) return '🌐';
	return String.fromCodePoint(FLAG_BASE + (cc.charCodeAt(0) - 65), FLAG_BASE + (cc.charCodeAt(1) - 65));
}

// mbps formats a download/upload number, or a dash when absent.
export function mbps(v) {
	return v == null ? '—' : `${v.toFixed(1)} MB/s`;
}

// ms formats a latency value, or a dash when absent.
export function ms(v) {
	return v == null ? '—' : `${v} ms`;
}

// ago renders a timestamp as a compact relative age (e.g. "3m", "2h").
export function ago(ts, now = Date.now()) {
	if (!ts) return '—';
	const secs = Math.max(0, Math.floor((now - new Date(ts).getTime()) / 1000));
	if (secs < 60) return `${secs}s`;
	if (secs < 3600) return `${Math.floor(secs / 60)}m`;
	if (secs < 86400) return `${Math.floor(secs / 3600)}h`;
	return `${Math.floor(secs / 86400)}d`;
}

// dur formats a duration in seconds as a compact string (e.g. "45s", "2m 30s",
// "1h 5m"). Negative/absent renders a dash.
export function dur(secs) {
	if (secs == null || secs < 0) return '—';
	secs = Math.round(secs);
	if (secs < 60) return `${secs}s`;
	const m = Math.floor(secs / 60);
	const s = secs % 60;
	if (m < 60) return s ? `${m}m ${s}s` : `${m}m`;
	const h = Math.floor(m / 60);
	return `${h}h ${m % 60}m`;
}

// statusClass maps a run/server status to a daisyUI badge modifier.
export function statusClass(status) {
	if (status === 'ok') return 'badge-success';
	if (!status) return 'badge-ghost';
	if (status === 'timeout') return 'badge-warning';
	return 'badge-error';
}
