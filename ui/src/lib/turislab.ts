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
	/** Null while the establishment waits for its first scan. */
	score: Score | null;
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
		if (filters.tier && (!item.score || item.score.tier !== filters.tier)) return false;
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

/** One establishment whose score changed between two report snapshots. */
export interface ScoreChange {
	id: string;
	name: string;
	tier: Tier;
	closureRisk: number;
	/** True when the establishment had no score before: its first scan. */
	first: boolean;
}

/**
 * The establishments whose score changed between two reports, in queue
 * order. A changed computed_at means the scanner re-scored the
 * establishment; the first load (prev null) reports nothing so landing on
 * the page stays quiet.
 */
export function scoreChanges(prev: TurislabReport | null, next: TurislabReport): ScoreChange[] {
	if (!prev) return [];
	const before = new Map(prev.queue.map((i) => [i.establishment.id, i.score?.computed_at]));
	const out: ScoreChange[] = [];
	for (const item of next.queue) {
		const was = before.get(item.establishment.id);
		if (item.score && item.score.computed_at !== was) {
			out.push({
				id: item.establishment.id,
				name: item.establishment.name,
				tier: item.score.tier,
				closureRisk: item.score.closure_risk,
				first: was === undefined
			});
		}
	}
	return out;
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

// ─── KPI / chart helpers ─────────────────────────────────────────────────────

/** Human-readable status metadata: label plus the design-system colour. */
export interface StatusMeta {
	label: string;
	color: string;
}

/** Suggested statuses in display order (worst first). */
export const STATUSES: SuggestedStatus[] = [
	'closure_confirmed',
	'closure_suspected',
	'closed_temporarily',
	'seasonal',
	'active',
	'unknown'
];

/** Status metadata for badges, charts and KPI chips. */
export const STATUS_META: Record<SuggestedStatus, StatusMeta> = {
	closure_confirmed: { label: 'Cierre confirmado', color: 'var(--color-ko)' },
	closure_suspected: { label: 'Cierre sospechado', color: 'var(--color-partial)' },
	closed_temporarily: { label: 'Cierre temporal', color: 'var(--color-empty)' },
	seasonal: { label: 'Estacional', color: 'var(--color-accent-3)' },
	active: { label: 'Activo', color: 'var(--color-ok)' },
	unknown: { label: 'Sin datos', color: 'var(--color-skipped)' }
};

/** Counts the queue per tier. */
export function tierCounts(queue: QueueItem[]): Record<Tier, number> {
	const counts: Record<Tier, number> = {
		P0: 0,
		P1: 0,
		P2: 0,
		P3: 0,
		collect_data: 0
	};
	for (const item of queue) if (item.score) counts[item.score.tier]++;
	return counts;
}

/** Counts the queue per suggested operational status. */
export function statusCounts(queue: QueueItem[]): Record<SuggestedStatus, number> {
	const counts = Object.fromEntries(STATUSES.map((s) => [s, 0])) as Record<
		SuggestedStatus,
		number
	>;
	for (const item of queue) if (item.score) counts[item.score.status]++;
	return counts;
}

/** One row of a grouped chart: a label, its total and the per-key breakdown. */
export interface GroupRow<K extends string> {
	label: string;
	total: number;
	counts: Record<K, number>;
}

function emptyCounts<K extends string>(keys: readonly K[]): Record<K, number> {
	return Object.fromEntries(keys.map((key) => [key, 0])) as Record<K, number>;
}

/**
 * Groups the queue by an arbitrary key of the establishment and counts one
 * dimension inside each group. The rows come sorted by total (worst first)
 * and, when `limit` is set, the tail is folded into a single "Otros" row.
 */
function groupQueue<K extends string>(
	queue: QueueItem[],
	groupOf: (item: QueueItem) => string,
	keyOf: (item: QueueItem) => K | undefined,
	keys: readonly K[],
	limit?: number
): GroupRow<K>[] {
	const groups = new Map<string, GroupRow<K>>();
	for (const item of queue) {
		// Unscored items are not classified yet; they belong to no tier/status
		// bucket, so the charts skip them until the first scan.
		const key = keyOf(item);
		if (!key) continue;
		const label = groupOf(item);
		let row = groups.get(label);
		if (!row) {
			row = { label, total: 0, counts: emptyCounts(keys) };
			groups.set(label, row);
		}
		row.total++;
		row.counts[key]++;
	}
	const rows = [...groups.values()].sort((a, b) => b.total - a.total || a.label.localeCompare(b.label));
	if (limit === undefined || rows.length <= limit) return rows;

	const kept = rows.slice(0, limit);
	const other: GroupRow<K> = { label: 'Otros', total: 0, counts: emptyCounts(keys) };
	for (const row of rows.slice(limit)) {
		other.total += row.total;
		for (const key of keys) other.counts[key] += row.counts[key];
	}
	return [...kept, other];
}

/** Establishments per municipality, split by tier (top `limit`, rest "Otros"). */
export function tierRowsByMunicipality(queue: QueueItem[], limit = 8): GroupRow<Tier>[] {
	return groupQueue(
		queue,
		(item) => item.establishment.municipality || 'Sin municipio',
		(item) => item.score?.tier,
		TIERS,
		limit
	);
}

/** Establishments per category, split by suggested status. */
export function statusRowsByCategory(queue: QueueItem[]): GroupRow<SuggestedStatus>[] {
	return groupQueue(
		queue,
		(item) => CATEGORY_LABELS[item.establishment.category],
		(item) => item.score?.status,
		STATUSES
	);
}

/** Closure-risk histogram in 20-point buckets, worst bucket last. */
export interface RiskBucket {
	label: string;
	count: number;
}

export function riskBuckets(queue: QueueItem[]): RiskBucket[] {
	const edges = [0, 0.2, 0.4, 0.6, 0.8, 1];
	const buckets: RiskBucket[] = [];
	for (let i = 0; i < edges.length - 1; i++) {
		const from = Math.round(edges[i] * 100);
		const to = Math.round(edges[i + 1] * 100);
		buckets.push({ label: `${from}–${to}%`, count: 0 });
	}
	for (const item of queue) {
		if (!item.score) continue;
		const risk = Math.min(Math.max(item.score.closure_risk, 0), 1);
		const index = Math.min(Math.floor(risk * (edges.length - 1)), buckets.length - 1);
		buckets[index].count++;
	}
	return buckets;
}

/** Establishments with at least one piece of evidence behind their score. */
export function coveredCount(report: TurislabReport): number {
	return Math.round((report.summary.coverage_pct / 100) * report.summary.total);
}

/** Formats an RFC3339 timestamp for the UI. */
export function formatTime(iso: string): string {
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return iso;
	return date.toLocaleString('es-ES', {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}
