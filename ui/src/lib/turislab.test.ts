import { describe, expect, it } from 'vitest';
import {
	filterQueue,
	pct,
	pocCriterionMet,
	riskLabel,
	scanAge,
	TIERS,
	TIER_COLORS,
	TIER_LABELS,
	type QueueItem,
	type Score
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

function item(rank: number, id: string, name: string, overrides: Partial<Score> = {}): QueueItem {
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
		score: score({ establishment_id: id, ...overrides }),
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
