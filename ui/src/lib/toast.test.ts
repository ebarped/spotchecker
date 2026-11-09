import { describe, expect, it } from 'vitest';
import { MAX_TOASTS, dismissToast, defaultTtl, pushToast, type Toast } from './toast';

function toast(id: number, title: string, kind: Toast['kind'] = 'info'): Toast {
	return { id, kind, title, ttl: 5000 };
}

describe('pushToast', () => {
	it('appends the toast and keeps the order', () => {
		const list = pushToast([], { kind: 'info', title: 'a' }, 1);
		expect(pushToast(list, { kind: 'ok', title: 'b' }, 2).map((t) => t.title)).toEqual(['a', 'b']);
	});

	it('replaces a toast with the same title instead of stacking it', () => {
		let list = pushToast([], { kind: 'info', title: 'sync' }, 1);
		list = pushToast(list, { kind: 'info', title: 'sync' }, 2);
		expect(list).toHaveLength(1);
		expect(list[0].id).toBe(2);
	});

	it('caps the stack at MAX_TOASTS, dropping the oldest', () => {
		let list: Toast[] = [];
		for (let i = 1; i <= MAX_TOASTS + 2; i++) {
			list = pushToast(list, { kind: 'info', title: `t${i}` }, i);
		}
		expect(list).toHaveLength(MAX_TOASTS);
		expect(list[0].title).toBe(`t3`);
		expect(list.at(-1)?.title).toBe(`t${MAX_TOASTS + 2}`);
	});

	it('defaults the lifetime per kind', () => {
		const list = pushToast(
			[],
			{ kind: 'error', title: 'x' },
			1
		);
		expect(list[0].ttl).toBe(defaultTtl('error'));
	});
});

describe('dismissToast', () => {
	it('removes by id and ignores unknown ids', () => {
		const list = [toast(1, 'a'), toast(2, 'b')];
		expect(dismissToast(list, 1).map((t) => t.id)).toEqual([2]);
		expect(dismissToast(list, 99)).toHaveLength(2);
	});
});
