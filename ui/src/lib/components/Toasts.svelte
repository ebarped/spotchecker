<script lang="ts">
	import { fly } from 'svelte/transition';
	import { toasts } from '../toast.svelte';
	import type { ToastKind } from '../toast';

	// The dot colour tells the kind at a glance: the same status palette the
	// rest of the design system uses.
	const dot: Record<ToastKind, string> = {
		ok: 'bg-ok',
		error: 'bg-ko',
		info: 'bg-accent'
	};
</script>

<div
	class="pointer-events-none fixed right-4 bottom-4 z-50 flex w-full max-w-sm flex-col gap-2"
	aria-live="polite"
>
	{#each toasts.list as t (t.id)}
		<div
			transition:fly={{ y: 12, duration: 180 }}
			class="glass pointer-events-auto flex items-start gap-3 rounded-xl px-4 py-3 shadow-lg"
			role="status"
		>
			<span class="mt-1.5 size-2 shrink-0 rounded-full {dot[t.kind]}" aria-hidden="true"></span>
			<div class="min-w-0 flex-1">
				<p class="text-sm font-medium text-ink">{t.title}</p>
				{#if t.body}
					<p class="mt-0.5 text-xs leading-relaxed text-muted">{t.body}</p>
				{/if}
			</div>
			<button
				type="button"
				class="-mt-1 -mr-1 shrink-0 rounded-lg p-1 text-faint transition-colors hover:bg-surface-2 hover:text-ink"
				aria-label="Cerrar el aviso"
				onclick={() => toasts.dismiss(t.id)}
			>
				<svg class="size-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
					<path stroke-linecap="round" stroke-linejoin="round" d="M6 6l12 12M18 6L6 18" />
				</svg>
			</button>
		</div>
	{/each}
</div>
