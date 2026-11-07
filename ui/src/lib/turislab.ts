/**
 * Types mirroring the turislab-doctor JSON report (app/internal/turislab/model),
 * plus the pure helpers the UI derives from it: tier metadata, queue filters
 * and pipeline phases. Everything here is unit-tested in turislab.test.ts.
 */

export type Tier = 'P0' | 'P1' | 'P2' | 'P3' | 'collect_data';

export type SuggestedStatus =
	| 'active'
	| 'closure_suspected'
	| 'closure_confirmed'
	| 'closed_temporarily'
	| 'seasonal'
	| 'unknown';

export type Category = 'restaurant' | 'hotel' | 'bar' | 'other';

export interface Establishment {
	id: string;
	name: string;
	category: Category;
	stars?: number | null;
	address: string;
	municipality: string;
	province: string;
	postal_code: string;
	country: string;
	lat?: number | null;
	lng?: number | null;
	phone: string;
	email: string;
	website: string;
	registry_id: string;
	notes: string;
	id_generated: boolean;
}

export interface Score {
	establishment_id: string;
	computed_at: string;
	closure_risk: number;
	quality_risk: number;
	quality_delta: number;
	priority: number;
	tier: Tier;
	confidence: number;
	evidence_count: number;
	status: SuggestedStatus;
	reason: string;
	volatile: boolean;
	conflict_detected: boolean;
}

export interface QueueItem {
	rank: number;
	establishment: Establishment;
	score: Score;
	primary_signals: unknown[];
	scan_age_ns: number;
}

export interface ReportSummary {
	total: number;
	p0: number;
	p1: number;
	p2: number;
	p3: number;
	collect_data: number;
	closure_confirmed: number;
	low_confidence: number;
	coverage_pct: number;
	scan_cost_eur: number;
}

export interface SourceStatus {
	name: string;
	enabled: boolean;
	healthy: boolean;
	last_error?: string;
	last_run_at?: string | null;
	signals_collected: number;
}

export interface TurislabReport {
	generated_at: string;
	establishment_count: number;
	queue: QueueItem[];
	summary: ReportSummary;
	scan_cursor: string;
	scan_running: boolean;
	scan_error?: string;
	sources: SourceStatus[];
}

export interface PipelinePhase {
	id: number;
	name: string;
	what: string;
	artifact: string;
}

export interface Pipeline {
	phases: PipelinePhase[];
}

export interface QueueFilters {
	tier?: Tier | '';
	category?: Category | '';
	municipality?: string;
	q?: string;
}

/** The tiers in display order, top priority first. */
export const TIERS: Tier[] = ['P0', 'P1', 'P2', 'P3', 'collect_data'];

/** Human-readable tier labels for the filter chips and the table badges. */
export const TIER_LABELS: Record<Tier, string> = {
	P0: 'P0 · Visitar ya',
	P1: 'P1 · Visitar pronto',
	P2: 'P2 · Programar visita',
	P3: 'P3 · Vigilar',
	collect_data: 'Recoger datos'
};

/** Tier accent colours, mapped onto the design-system palette. */
export const TIER_COLORS: Record<Tier, string> = {
	P0: 'var(--color-ko)',
	P1: 'var(--color-partial)',
	P2: 'var(--color-empty)',
	P3: 'var(--color-ok)',
	collect_data: 'var(--color-skipped)'
};

/** Human-readable labels for the suggested operational status. */
export const STATUS_LABELS: Record<SuggestedStatus, string> = {
	active: 'Activo',
	closure_suspected: 'Cierre sospechado',
	closure_confirmed: 'Cierre confirmado',
	closed_temporarily: 'Cierre temporal',
	seasonal: 'Estacional',
	unknown: 'Sin datos'
};

/** Human-readable category labels. */
export const CATEGORY_LABELS: Record<Category, string> = {
	restaurant: 'Restaurante',
	hotel: 'Hotel',
	bar: 'Bar',
	other: 'Otro'
};

/**
 * Filters the queue by tier, category, municipality and free text on the
 * name, preserving the rank order the backend already applied.
 */
export function filterQueue(queue: QueueItem[], filters: QueueFilters): QueueItem[] {
	const q = (filters.q ?? '').trim().toLowerCase();
	return queue.filter((item) => {
		if (filters.tier && item.score.tier !== filters.tier) return false;
		if (filters.category && item.establishment.category !== filters.category) return false;
		if (
			filters.municipality &&
			item.establishment.municipality.toLowerCase() !== filters.municipality.toLowerCase()
		)
			return false;
		if (q && !item.establishment.name.toLowerCase().includes(q)) return false;
		return true;
	});
}

/** Formats a fraction (0–1) as a percentage with no decimals. */
export function pct(fraction: number): string {
	return `${Math.round(fraction * 100)}%`;
}

/** Formats a 0–1 risk as a percentage for display. */
export function riskLabel(risk: number): string {
	return `${Math.round(risk * 100)}%`;
}

/** Humanises the scan age (nanoseconds from the API) as "hace X". */
export function scanAge(scanAgeNs: number): string {
	const ms = scanAgeNs / 1e6;
	if (ms < 60_000) return 'hace instantes';
	if (ms < 3_600_000) return `hace ${Math.round(ms / 60_000)} min`;
	if (ms < 86_400_000) return `hace ${Math.round(ms / 3_600_000)} h`;
	return `hace ${Math.round(ms / 86_400_000)} d`;
}

/**
 * True when the report satisfies the PoC success criterion: every
 * ground-truth closed establishment sits within the top 3 ranks. Used by the
 * validation banner; the closed IDs are supplied by the operator.
 */
export function pocCriterionMet(queue: QueueItem[], closedIds: string[]): boolean {
	if (closedIds.length === 0) return false;
	return closedIds.every((id) => {
		const item = queue.find((q) => q.establishment.id === id);
		return item !== undefined && item.rank <= 3;
	});
}
