<script lang="ts">
	import { SvelteFlow, Controls, type Node, type Edge } from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { agent } from '../../agent.svelte';
	import AutoFit from './AutoFit.svelte';
	import FlowNode from './FlowNode.svelte';

	let { ready }: { ready: boolean } = $props();

	$effect(() => {
		void agent.loadCatalog();
	});

	const nodeTypes = { flow: FlowNode };

	let width = $state(0);
	let height = $state(0);

	/** The traversal as Svelte Flow nodes: one per event, stacked vertically. */
	const flowNodes = $derived.by<Node[]>(() =>
		agent.flow.map((node, index) => ({
			id: String(index),
			type: 'flow',
			position: { x: 0, y: index * 96 },
			data: {
				label:
					node.kind === 'user'
						? 'Pregunta'
						: node.kind === 'answer'
							? 'Respuesta'
							: `tool: ${node.name ?? ''}`,
				category: node.kind === 'user' ? 'Pregunta' : node.kind === 'answer' ? 'answer' : (node.category ?? 'tool'),
				description: node.description ?? ''
			}
		}))
	);

	const flowEdges = $derived.by<Edge[]>(() =>
		agent.flow.slice(0, -1).map((_, index) => ({
			id: `e${index}`,
			source: String(index),
			target: String(index + 1),
			type: 'straight',
			style: 'stroke: var(--gd-muted); stroke-width: 1.4px',
			markerEnd: { type: 'arrowclosed', color: 'var(--gd-muted)' }
		}))
	);
</script>

<!-- The whole card is the gesture zone: the chart canvas covers it edge to
     edge, so wheel zoom and drag pan work everywhere. -->
<section
	class="glass relative flex min-h-[180px] flex-1 flex-col overflow-hidden rounded-2xl p-2"
>
	<p
		class="pointer-events-none absolute left-3 top-2 z-10 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-ink"
	>
		<span class="size-1.5 rounded-full bg-accent-2"></span>
		Ruta del agente
	</p>
	<!-- Delimited by the top: the canvas starts under the title, so the
	     graph never slides behind it — the border-t is the delimiter. -->
	<div
		class="absolute inset-x-0 bottom-0 border-t border-line-strong"
		style="top: 34px"
		bind:clientWidth={width}
		bind:clientHeight={height}
	>
		{#if flowNodes.length === 0}
			<div
				class="absolute inset-0 flex items-center justify-center px-8 text-center text-xs leading-relaxed text-faint"
			>
				Aquí aparece el recorrido del agente — la pregunta, las herramientas que llamó y la
				respuesta, encadenados en el orden en que se ejecutaron.
			</div>
		{:else}
			<SvelteFlow
				nodes={flowNodes}
				edges={flowEdges}
				{nodeTypes}
				fitView
				fitViewOptions={{ padding: 0.2 }}
				nodesDraggable={false}
				edgesFocusable={false}
				nodesConnectable={false}
				proOptions={{ hideAttribution: true }}
				minZoom={0.3}
				maxZoom={2.5}
			>
				<Controls position="bottom-right" showZoom={false} showFitView={true} showLock={false} />
				<AutoFit trigger={flowNodes.length} />
			</SvelteFlow>
		{/if}
	</div>
</section>
