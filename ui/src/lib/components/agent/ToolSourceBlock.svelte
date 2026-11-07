<script lang="ts">
	import type { AgentTool, AgentToolSource } from '../../agent.svelte';

	let { source }: { source: AgentToolSource } = $props();

	// The description tooltip is fixed-positioned so it escapes the sidebar's
	// scroll clipping and opens over the chat, out of the way.
	let tip = $state<{ tool: AgentTool; x: number; y: number } | null>(null);

	function show(tool: AgentTool, event: MouseEvent) {
		const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
		tip = {
			tool,
			x: rect.right + 10,
			y: Math.min(rect.top, window.innerHeight - 160)
		};
	}
</script>

<details class="group">
	<summary
		class="flex cursor-pointer list-none items-center justify-between rounded-lg px-2 py-1.5 text-xs font-medium text-muted transition-colors hover:bg-surface-2 hover:text-ink [&::-webkit-details-marker]:hidden"
	>
		<span class="truncate">{source.label}</span>
		{#if source.tools.length > 0}
			<span class="rounded-full bg-accent/10 px-1.5 py-0.5 text-[10px] font-semibold text-accent">
				{source.tools.length}
			</span>
		{/if}
	</summary>
	{#if source.error}
		<p class="px-2 py-1 text-[11px] text-error">{source.error}</p>
	{:else}
		<ul class="mb-2 mt-1 space-y-0.5" onmouseleave={() => (tip = null)}>
			{#each source.tools as tool (tool.name)}
				<li
					class="mono cursor-help rounded-md px-2 py-1 text-[11px] text-faint transition-colors hover:bg-accent/10 hover:text-accent"
					onmouseenter={(event) => show(tool, event)}
				>
					{tool.name}
				</li>
			{/each}
		</ul>
	{/if}
	{#if tip}
		<div
			class="pointer-events-none fixed z-50 w-72 rounded-xl border border-line-strong bg-overlay p-3 shadow-xl"
			style="left: {tip.x}px; top: {tip.y}px;"
		>
			<p class="mono text-[11px] font-semibold text-accent">{tip.tool.name}</p>
			<p class="mt-1 text-[11px] leading-relaxed text-muted">{tip.tool.description}</p>
		</div>
	{/if}
</details>
