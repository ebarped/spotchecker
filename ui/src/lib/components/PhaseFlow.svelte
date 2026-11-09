<script lang="ts">
	import type { FlowStep } from '../pipeline';
	import StatusChip from './StatusChip.svelte';

	let { steps }: { steps: FlowStep[] } = $props();
</script>

<ol class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
	{#each steps as step, i (step.title)}
		<li class="glass relative flex flex-col rounded-xl p-4">
			<div class="flex items-center justify-between gap-2">
				<span
					class="flex size-7 shrink-0 items-center justify-center rounded-full bg-surface-2 font-mono text-xs font-semibold text-accent"
				>
					{i + 1}
				</span>
				{#if step.status}
					<StatusChip status={step.status} />
				{/if}
			</div>
			<p class="mt-3 text-sm font-semibold">{step.title}</p>
			<p class="mt-1 text-xs leading-relaxed text-muted">{step.body}</p>
		</li>
	{/each}
</ol>
