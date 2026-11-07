/**
 * The app is a pure SPA (ssr = false): every module only ever evaluates in
 * the browser, except possibly at build time, hence the guard instead of
 * depending on $app/environment.
 */
const browser = typeof document !== 'undefined';

export type Theme = 'dark' | 'light';

const THEME_KEY = 'turislab-doctor:theme';

/** Reads the persisted theme, defaulting to the dark aurora. */
function initialTheme(): Theme {
	if (!browser) return 'dark';
	const stored = localStorage.getItem(THEME_KEY);
	return stored === 'light' ? 'light' : 'dark';
}

/**
 * Keeps `<html data-theme>` in sync so Tailwind's `dark:` variant and the
 * CSS variables in app.css switch together. The init script in app.html
 * applies the attribute before first paint; this store only has to maintain
 * it afterwards.
 */
class ThemeController {
	theme = $state<Theme>(initialTheme());

	toggle(): void {
		this.theme = this.theme === 'dark' ? 'light' : 'dark';
		if (browser) {
			document.documentElement.dataset.theme = this.theme;
			localStorage.setItem(THEME_KEY, this.theme);
		}
	}
}

export const theme = new ThemeController();
