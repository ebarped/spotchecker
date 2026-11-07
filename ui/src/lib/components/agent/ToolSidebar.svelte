<script lang="ts">
	import { agent } from '../../agent.svelte';
	import ToolSourceBlock from './ToolSourceBlock.svelte';

	const total = $derived(
		(agent.catalog?.builtin ?? []).reduce((n, s) => n + s.tools.length, 0) +
			(agent.catalog?.external ?? []).reduce((n, s) => n + s.tools.length, 0)
	);
</script>

<aside class="glass flex flex-col rounded-2xl lg:h-[45%] lg:overflow-y-auto">
	<div class="flex items-baseline justify-between px-4 pt-3 pb-2.5">
		<p class="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-ink">
			<span class="size-1.5 rounded-full bg-accent"></span>
			Tools
		</p>
		{#if agent.catalog}
			<p class="text-[11px] text-faint">{total} loaded</p>
		{/if}
	</div>
	<div class="border-t border-line-strong px-4 py-3">

	{#if !agent.catalog}
		<p class="text-sm text-muted">Loading the tool catalog…</p>
	{:else}
		<section class="mb-4">
			<p class="caption mb-1">Built-in</p>
			<div class="space-y-1">
				{#each agent.catalog.builtin as source (source.key)}
					<ToolSourceBlock {source} />
				{/each}
			</div>
		</section>

		{#if agent.catalog.external.length > 0}
			<section>
				<p class="caption mb-1">External MCP servers</p>
				<div class="space-y-1">
					{#each agent.catalog.external as source (source.key)}
						<ToolSourceBlock {source} />
					{/each}
				</div>
			</section>
		{:else}
			<p class="px-2 text-[11px] text-faint">
				No external MCP servers configured — set them under <span class="mono">agent.mcp</span> in the config file.
			</p>
		{/if}
	{/if}
	</div>
</aside>
