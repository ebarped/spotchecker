import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// Where the turislab server (--addr) lives during `npm run dev`.
// The dev server proxies /api/* there so the UI always reads a live report.
const doctor = process.env.DOCTOR_URL ?? 'http://localhost:8090';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// Static build: the SPA fetches /api/report from whatever serves it.
			// The output goes straight into the Go module's embed folder
			// (../app/ui/build), which `go:embed` picks up at compile time.
			adapter: adapter({ pages: '../app/ui/build', assets: '../app/ui/build' })
		})
	],
	server: {
		proxy: {
			'/api': {
				target: doctor,
				changeOrigin: true
			}
		}
	}
});
