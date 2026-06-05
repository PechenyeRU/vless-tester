import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
export default {
	preprocess: vitePreprocess(),
	kit: {
		// SPA: a single index.html fallback the coordinator serves for every route,
		// with client-side routing taking over. The build output is embedded into
		// the coordinator binary (web/embed.go).
		adapter: adapter({ fallback: 'index.html', pages: 'build', assets: 'build' }),
		paths: { relative: true }
	}
};
