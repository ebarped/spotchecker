/**
 * The reactive toast controller: a thin $state wrapper over the pure logic
 * in toast.ts. Pure SPA — the module only ever evaluates in the browser.
 */
import { dismissToast, pushToast, type Toast, type ToastInput } from './toast';

class ToastController {
	list = $state<Toast[]>([]);
	private seq = 0;
	private timers = new Map<number, ReturnType<typeof setTimeout>>();

	/** Pushes a toast; repeated titles replace the older entry in place. */
	push(input: ToastInput): void {
		this.seq += 1;
		const id = this.seq;
		this.list = pushToast(this.list, input, id);
		const toast = this.list.find((t) => t.id === id);
		if (toast && toast.ttl > 0) {
			this.timers.set(id, setTimeout(() => this.dismiss(id), toast.ttl));
		}
	}

	ok(title: string, body?: string): void {
		this.push({ kind: 'ok', title, body });
	}

	error(title: string, body?: string): void {
		this.push({ kind: 'error', title, body });
	}

	info(title: string, body?: string): void {
		this.push({ kind: 'info', title, body });
	}

	/** Manual dismissal; also clears the auto-dismiss timer. */
	dismiss(id: number): void {
		const timer = this.timers.get(id);
		if (timer) {
			clearTimeout(timer);
			this.timers.delete(id);
		}
		this.list = dismissToast(this.list, id);
	}
}

export const toasts = new ToastController();
