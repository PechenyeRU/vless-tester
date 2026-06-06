import { describe, it, expect } from 'vitest';
import { buildServerQuery } from './api.js';
import { flag, mbps, ms, ago } from './format.js';

describe('buildServerQuery', () => {
	it('omits empty fields', () => {
		expect(buildServerQuery({})).toBe('');
		expect(buildServerQuery({ country: '', minSpeed: 0 })).toBe('');
	});
	it('encodes the populated filter', () => {
		expect(
			buildServerQuery({
				country: 'FR',
				worker: 'w1',
				minSpeed: 5.5,
				q: 'fr1',
				sort: 'latency',
				dir: 'asc',
				page: 2,
				perPage: 50
			})
		).toBe('?country=FR&worker=w1&min_speed=5.5&q=fr1&sort=latency&dir=asc&page=2&per_page=50');
	});
});

describe('formatters', () => {
	it('renders country flags', () => {
		expect(flag('FR')).toBe('🇫🇷');
		expect(flag('')).toBe('🌐');
		expect(flag('xx')).toBe('🇽🇽');
	});
	it('formats speed and latency with a dash for null', () => {
		expect(mbps(12.34)).toBe('12.3 MB/s');
		expect(mbps(null)).toBe('-');
		expect(ms(42)).toBe('42 ms');
		expect(ms(null)).toBe('-');
	});
	it('renders relative ages', () => {
		const now = new Date('2026-06-05T12:00:00Z').getTime();
		expect(ago(new Date('2026-06-05T11:58:00Z').toISOString(), now)).toBe('2m');
		expect(ago(null, now)).toBe('-');
	});
});
