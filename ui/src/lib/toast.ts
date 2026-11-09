/**
 * Pure toast logic: how a push folds into the list, how a dismissal filters
 * it out and the cap that keeps the stack readable. Unit-tested in
 * toast.test.ts; the reactive controller lives in toast.svelte.ts.
 */

export type ToastKind = 'ok' | 'error' | 'info';

export interface Toast {
	id: number;
	kind: ToastKind;
	title: string;
	body?: string;
	/** Milliseconds the toast stays up; 0 keeps it until dismissed. */
	ttl: number;
}

export interface ToastInput {
	kind: ToastKind;
	title: string;
	body?: string;
	/** Overrides the default per-kind lifetime. */
	ttl?: number;
}

/** The stack never grows past this: more would cover the page content. */
export const MAX_TOASTS = 4;

/** Default lifetime per kind: errors need more reading time. */
export function defaultTtl(kind: ToastKind): number {
	return kind === 'error' ? 8000 : 5000;
}

/**
 * Folds one toast into the list. A toast with the same title replaces the
 * older one — a repeated event updates in place instead of stacking — and
 * the oldest entries fall off once the cap is hit.
 */
export function pushToast(list: Toast[], input: ToastInput, id: number): Toast[] {
	const toast: Toast = {
		id,
		kind: input.kind,
		title: input.title,
		body: input.body,
		ttl: input.ttl ?? defaultTtl(input.kind)
	};
	const kept = list.filter((t) => t.title !== input.title);
	kept.push(toast);
	return kept.slice(-MAX_TOASTS);
}

/** Removes one toast by id; unknown ids are a no-op. */
export function dismissToast(list: Toast[], id: number): Toast[] {
	return list.filter((t) => t.id !== id);
}
