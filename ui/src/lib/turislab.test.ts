import { describe, expect, it } from 'vitest';
import {
	filterQueue,
	pct,
	pocCriterionMet,
	riskLabel,
	scanAge,
	STATUSES,
	statusCounts,
	statusRowsByCategory,
	TIERS,
	TIER_COLORS,
	TIER_LABELS,
	tierCounts,
	tierRowsByMunicipality,
	coveredCount,
	formatTime,
	riskBuckets,
	type QueueItem,
	type Score,
	type TurislabReport
} from './turislab';

function score(overrides: Partial<Score> = {}): Score {
	return {
		establishment_id: 'est-1',
		computed_at: '2026-11-05T12:00:00Z',
		closure_risk: 0.1,
		quality_risk: 0,
		quality_delta: 0,
		priority: 10,
		tier: 'P3',
		confidence: 0.7,
		evidence_count: 2,
		status: 'active',
		reason: 'Sin indicios',
		volatile: false,
		conflict_detected: false,
		...overrides
	};
}

function item(
	rank: number,
	id: string,
	name: string,
	overrides: Partial<Score> | null = {}
): QueueItem {
	return {
		rank,
		establishment: {
			id,
			name,
			category: 'restaurant',
			address: '',
			municipality: 'Alicante',
			province: '',
			postal_code: '',
			country: 'ES',
			phone: '',
			email: '',
			website: '',
			registry_id: '',
			notes: '',
			id_generated: false
		},
		score: overrides === null ? null : score({ establishment_id: id, ...overrides }),
		primary_signals: [],
		scan_age_ns: 0
	};
}

const queue: QueueItem[] = [
	item(1, 'rest-002', 'La Tasca Cerrada', { tier: 'P0', priority: 92 }),
	item(2, 'hotel-002', 'Hostal El Pinar', { tier: 'P1', priority: 74 }),
	item(3, 'rest-001', 'Restaurante El Ancla', { tier: 'P3', priority: 8 }),
	item(4, 'bar-001', 'Bar La Esquina', { tier: 'collect_data', priority: 0 })
];

describe('filterQueue', () => {
	it('keeps the rank order untouched', () => {
		const out = filterQueue(queue, {});
		expect(out.map((i) => i.rank)).toEqual([1, 2, 3, 4]);
	});

	it('filters by tier', () => {
		const out = filterQueue(queue, { tier: 'P0' });
		expect(out).toHaveLength(1);
		expect(out[0].establishment.id).toBe('rest-002');
	});

	it('filters by free text on the name, case-insensitively', () => {
		expect(filterQueue(queue, { q: 'tasca' }).map((i) => i.establishment.id)).toEqual(['rest-002']);
		expect(filterQueue(queue, { q: 'EL' }).length).toBe(2);
	});

	it('combines filters', () => {
		const out = filterQueue(queue, { tier: 'P0', q: 'pinar' });
		expect(out).toHaveLength(0);
	});

	it('trims the query', () => {
		expect(filterQueue(queue, { q: '  ancla  ' })).toHaveLength(1);
	});

	it('returns everything with empty filters', () => {
		expect(filterQueue(queue, { tier: '', q: '' })).toHaveLength(4);
	});

	it('keeps unscored items only when no tier filter is set', () => {
		const waiting = [item(1, 'rest-001', 'La Oca', null), ...queue];
		expect(filterQueue(waiting, {})).toHaveLength(5);
		expect(filterQueue(waiting, { tier: 'P0' })).toHaveLength(1);
	});
});

describe('pocCriterionMet', () => {
	it('passes when both closed establishments sit in the top 3', () => {
		expect(pocCriterionMet(queue, ['rest-002', 'hotel-002'])).toBe(true);
	});

	it('fails when a closed establishment falls below rank 3', () => {
		expect(pocCriterionMet(queue, ['rest-002', 'bar-001'])).toBe(false);
	});

	it('fails when a closed id is missing from the queue', () => {
		expect(pocCriterionMet(queue, ['rest-002', 'rest-999'])).toBe(false);
	});

	it('fails with an empty closed list (nothing to validate)', () => {
		expect(pocCriterionMet(queue, [])).toBe(false);
	});
});

describe('display helpers', () => {
	it('formats risks and fractions', () => {
		expect(riskLabel(0.826)).toBe('83%');
		expect(pct(0.5)).toBe('50%');
		expect(pct(0.999)).toBe('100%');
	});

	it('humanises scan ages', () => {
		expect(scanAge(5 * 1e9)).toBe('hace instantes');
		expect(scanAge(5 * 60 * 1e9)).toBe('hace 5 min');
		expect(scanAge(2 * 3_600 * 1e9)).toBe('hace 2 h');
		expect(scanAge(3 * 86_400 * 1e9)).toBe('hace 3 d');
	});

	it('labels every tier and gives every tier a colour', () => {
		for (const tier of TIERS) {
			expect(TIER_LABELS[tier]).toBeTruthy();
			expect(TIER_COLORS[tier]).toBeTruthy();
		}
	});
});

describe('tierCounts and statusCounts', () => {
	it('counts the queue per tier', () => {
		expect(tierCounts(queue)).toEqual({ P0: 1, P1: 1, P2: 0, P3: 1, collect_data: 1 });
	});

	it('skips unscored items', () => {
		const waiting = [item(1, 'a', 'A', null), ...queue];
		expect(tierCounts(waiting)).toEqual({ P0: 1, P1: 1, P2: 0, P3: 1, collect_data: 1 });
		const counts = statusCounts(waiting);
		expect(counts.active).toBe(4);
		expect(counts.unknown).toBe(0);
	});

	it('counts the queue per status, including the empty ones', () => {
		const counts = statusCounts(queue);
		expect(counts.active).toBe(4);
		for (const status of STATUSES) expect(counts[status]).toBeTypeOf('number');
	});

	it('counts every status independently', () => {
		const mixed = [
			item(1, 'a', 'A', { status: 'closure_confirmed' }),
			item(2, 'b', 'B', { status: 'closure_suspected' }),
			item(3, 'c', 'C', { status: 'active' }),
			item(4, 'd', 'D', { status: 'active' })
		];
		const counts = statusCounts(mixed);
		expect(counts.closure_confirmed).toBe(1);
		expect(counts.closure_suspected).toBe(1);
		expect(counts.active).toBe(2);
		expect(counts.unknown).toBe(0);
	});
});

describe('grouped chart rows', () => {
	it('groups by municipality, sorted by total and split by tier', () => {
		const mixed = [
			item(1, 'a', 'A', { tier: 'P0' }),
			item(2, 'b', 'B', { tier: 'P3' }),
			item(3, 'c', 'C', { tier: 'P0' })
		];
		mixed[0].establishment.municipality = 'Benidorm';
		mixed[1].establishment.municipality = 'Benidorm';
		mixed[2].establishment.municipality = 'Alicante';

		const rows = tierRowsByMunicipality(mixed);
		expect(rows[0].label).toBe('Benidorm');
		expect(rows[0].total).toBe(2);
		expect(rows[0].counts.P0).toBe(1);
		expect(rows[0].counts.P3).toBe(1);
		expect(rows[1].label).toBe('Alicante');
	});

	it('falls back to a label when the municipality is empty', () => {
		const lonely = [item(1, 'a', 'A')];
		lonely[0].establishment.municipality = '';
		expect(tierRowsByMunicipality(lonely)[0].label).toBe('Sin municipio');
	});

	it('skips unscored items in the grouped charts', () => {
		const mixed = [item(1, 'a', 'A', { tier: 'P0' }), item(2, 'b', 'B', null)];
		const rows = tierRowsByMunicipality(mixed);
		expect(rows).toHaveLength(1);
		expect(rows[0].total).toBe(1);
	});

	it('folds the tail into a single Otros row when limited', () => {
		const many = Array.from({ length: 12 }, (_, i) => {
			const entry = item(i + 1, `est-${i}`, `Est ${i}`);
			entry.establishment.municipality = `Municipio ${i}`;
			return entry;
		});
		const rows = tierRowsByMunicipality(many, 4);
		expect(rows).toHaveLength(5);
		expect(rows[4].label).toBe('Otros');
		expect(rows[4].total).toBe(8);
	});

	it('groups by category, split by suggested status', () => {
		const mixed = [
			item(1, 'a', 'A', { status: 'closure_confirmed' }),
			item(2, 'b', 'B', { status: 'active' }),
			item(3, 'c', 'C', { status: 'active' })
		];
		mixed[0].establishment.category = 'hotel';
		mixed[1].establishment.category = 'hotel';
		mixed[2].establishment.category = 'restaurant';

		const rows = statusRowsByCategory(mixed);
		expect(rows[0].label).toBe('Hotel');
		expect(rows[0].total).toBe(2);
		expect(rows[0].counts.closure_confirmed).toBe(1);
		expect(rows[0].counts.active).toBe(1);
		expect(rows[1].label).toBe('Restaurante');
	});
});

describe('riskBuckets', () => {
	it('builds the five 20-point buckets', () => {
		expect(riskBuckets([]).map((b) => b.label)).toEqual([
			'0–20%',
			'20–40%',
			'40–60%',
			'60–80%',
			'80–100%'
		]);
	});

	it('places each risk in the right bucket', () => {
		const mixed = [
			item(1, 'a', 'A', { closure_risk: 0 }),
			item(2, 'b', 'B', { closure_risk: 0.19 }),
			item(3, 'c', 'C', { closure_risk: 0.2 }),
			item(4, 'd', 'D', { closure_risk: 0.5 }),
			item(5, 'e', 'E', { closure_risk: 0.99 }),
			item(6, 'f', 'F', { closure_risk: 1 })
		];
		expect(riskBuckets(mixed).map((b) => b.count)).toEqual([2, 1, 1, 0, 2]);
	});

	it('clamps values outside [0, 1]', () => {
		const mixed = [
			item(1, 'a', 'A', { closure_risk: -0.5 }),
			item(2, 'b', 'B', { closure_risk: 1.7 })
		];
		expect(riskBuckets(mixed).map((b) => b.count)).toEqual([1, 0, 0, 0, 1]);
	});
});

describe('coveredCount', () => {
	it('derives the covered establishments from the coverage percentage', () => {
		const report = { summary: { total: 200, coverage_pct: 42.5 } } as TurislabReport;
		expect(coveredCount(report)).toBe(85);
	});

	it('is zero with an empty report', () => {
		const report = { summary: { total: 0, coverage_pct: 0 } } as TurislabReport;
		expect(coveredCount(report)).toBe(0);
	});
});

describe('formatTime', () => {
	it('keeps an unparseable timestamp as is', () => {
		expect(formatTime('not-a-date')).toBe('not-a-date');
	});

	it('renders a valid timestamp', () => {
		expect(formatTime('2026-11-05T12:00:00Z')).toMatch(/2026/);
	});
});
