/**
 * Shared ECharts plumbing: the chart palette (read from the app.css theme
 * variables so charts match the dark canvas) and the option
 * fragments every chart builds on (typography, tooltip chrome, legend).
 */

export interface ChartPalette {
	ink: string;
	muted: string;
	faint: string;
	line: string;
	lineStrong: string;
	/** Tooltip background (the opaque overlay). */
	overlay: string;
	fontFamily: string;
	/** Status colours of the design system, as concrete values: ECharts
	 * paints on canvas, where CSS variables do not resolve. */
	ok: string;
	partial: string;
	ko: string;
	empty: string;
	error: string;
	skipped: string;
	/** Seasonal accent (theme dependent, unlike the status palette). */
	accent3: string;
}

/** The font stack is fixed; only colors differ per theme. */
const FONT = "'Inter', ui-sans-serif, system-ui, sans-serif";

/** Reads the resolved theme colors off :root. */
export function chartPalette(): ChartPalette {
	if (typeof document === 'undefined') {
		return {
			ink: '#f2f2f0',
			muted: '#a9aeb8',
			faint: '#7d828c',
			line: 'rgb(255 255 255 / 0.08)',
			lineStrong: 'rgb(255 255 255 / 0.16)',
			overlay: '#22252c',
			fontFamily: FONT,
			ok: '#73c68a',
			partial: '#f0b64e',
			ko: '#e8705e',
			empty: '#7fa8db',
			error: '#e58f8a',
			skipped: '#8b919c',
			accent3: '#e6bc6a'
		};
	}
	const style = getComputedStyle(document.documentElement);
	const read = (name: string) => style.getPropertyValue(name).trim();
	return {
		ink: read('--gd-ink'),
		muted: read('--gd-muted'),
		faint: read('--gd-faint'),
		line: read('--gd-line'),
		lineStrong: read('--gd-line-strong'),
		overlay: read('--gd-overlay'),
		fontFamily: FONT,
		ok: read('--color-ok'),
		partial: read('--color-partial'),
		ko: read('--color-ko'),
		empty: read('--color-empty'),
		error: read('--color-error'),
		skipped: read('--color-skipped'),
		accent3: read('--gd-accent-3')
	};
}

/** Tooltip chrome shared by every chart. confine keeps the popup inside
 * its chart rect — charts in narrow cards (the agent graph) would
 * otherwise spill the tooltip off the screen. */
export function tooltip(pal: ChartPalette) {
	return {
		trigger: 'item' as const,
		confine: true,
		backgroundColor: pal.overlay,
		borderColor: pal.lineStrong,
		borderWidth: 1,
		padding: [8, 12],
		textStyle: { color: pal.ink, fontFamily: pal.fontFamily, fontSize: 12 },
		extraCssText:
			'border-radius: 8px; box-shadow: 0 8px 24px rgb(0 0 0 / 0.35); max-width: 260px; white-space: normal;'
	};
}

/** Legend chrome shared by every chart — bottom-left or top-right (design
 * system default, like the Grafana panels this look comes from). */
export function legend(pal: ChartPalette, at: 'bottom' | 'top-right' = 'bottom') {
	const base = {
		icon: 'circle',
		itemWidth: 8,
		itemHeight: 8,
		itemGap: 16,
		textStyle: { color: pal.muted, fontFamily: pal.fontFamily, fontSize: 11 }
	};
	return at === 'bottom' ? { ...base, bottom: 0 } : { ...base, top: 0, right: 0 };
}

/** Muted value-axis styling shared by the bar charts. */
export function valueAxis(pal: ChartPalette) {
	return {
		type: 'value' as const,
		splitLine: { lineStyle: { color: pal.line } },
		axisLabel: { color: pal.faint, fontFamily: pal.fontFamily, fontSize: 10 },
		axisLine: { show: false },
		axisTick: { show: false }
	};
}

/** Category-axis styling shared by the bar charts. `labelWidth` wraps long
 * category names onto several lines within that width instead of the
 * text being cut off; omit it when labels are known to be short. */
export function categoryAxis(labels: string[], pal: ChartPalette, labelWidth?: number) {
	return {
		type: 'category' as const,
		data: labels,
		axisLabel: {
			color: pal.muted,
			fontFamily: pal.fontFamily,
			fontSize: 11,
			interval: 0,
			...(labelWidth === undefined
				? {}
				: { width: labelWidth, overflow: 'break' as const, lineHeight: 14 })
		},
		axisTick: { show: false },
		axisLine: { show: false }
	};
}
