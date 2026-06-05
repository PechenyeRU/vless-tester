import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		// Dev proxy: forward API calls to the coordinator so the SPA runs against a
		// live control plane during development.
		proxy: {
			'/api': {
				target: process.env.COORDINATOR_URL || 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
