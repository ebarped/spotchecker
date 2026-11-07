<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';

	let {
		data
	}: {
		data: { label: string; category: 'question' | 'tool' | 'answer' | 'error'; description?: string };
	} = $props();

	// One warm accent per interaction type.
	const COLORS: Record<string, string> = {
		question: '#4ea8dc',
		tool: '#e8875e',
		answer: '#e6bc6a',
		error: '#e8705e'
	};

	/** Ink or paper for the label, per how light the node fill is. */
	// The old tint scheme: subtle fill, colored label — the accent IS the ink.
	const color = $derived(COLORS[data.category] ?? '#e8875e');
</script>

<Handle type="target" position={Position.Top} />
<div
	class="flex items-center justify-center rounded-md border px-2 py-2 text-center text-[11px] font-semibold leading-tight"
	style="background-color: {color}1c; border-color: {color}55; color: {color}"
	title={data.description}
>
	{data.label}
</div>
<Handle type="source" position={Position.Bottom} />

<style>
	/* The node box sizes to its content; Svelte Flow measures it. */
	div {
		min-width: 84px;
		max-width: 140px;
	}
</style>
