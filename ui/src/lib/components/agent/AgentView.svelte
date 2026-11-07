<script lang="ts">
	import { agent } from '../../agent.svelte';
	import AgentChat from './AgentChat.svelte';
	import ToolGraph from './ToolGraph.svelte';
	import ToolSidebar from './ToolSidebar.svelte';

	let { ready }: { ready: boolean } = $props();

	$effect(() => {
		void agent.loadCatalog();
	});
</script>

<div class="grid items-stretch gap-4 lg:grid-cols-[280px_1fr]">
	<div class="flex flex-col gap-4 lg:h-[72vh]">
		<ToolSidebar />
		<ToolGraph {ready} />
	</div>
	<AgentChat {ready} />
</div>
