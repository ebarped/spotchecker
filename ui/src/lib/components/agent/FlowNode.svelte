<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';

	let {
		data
	}: {
		data: { label: string; category: 'question' | 'tool' | 'answer' | 'error'; description?: string };
	} = $props();

	// One accent per interaction type: the question, the tool call it
	// triggers and the answer it produces must read as different boxes at a
	// glance, so each kind gets its own hue.
	const COLORS: Record<string, string> = {
		question: '#4ea8dc',
		tool: '#e8875e',
		answer: '#73c68a',
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
	/* The node box sizes to its content; Svelte Flow measures it. Long tool
	   names (get_establishment_detail) must fit on one line, and any that do
	   not still break instead of overflowing the box. */
	div {
		min-width: 84px;
		max-width: 200px;
		overflow-wrap: anywhere;
	}
</style>
