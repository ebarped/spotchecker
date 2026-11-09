<script lang="ts">
	import { PIPELINE_DETAILS } from '../pipeline';

	let { current }: { current: string } = $props();
</script>

<nav
	class="glass flex gap-1 overflow-x-auto rounded-2xl p-1 text-xs sm:rounded-full"
	aria-label="Fases del pipeline"
>
	{#each PIPELINE_DETAILS as phase (phase.id)}
		<a
			href="/how-it-works/{phase.slug}"
			aria-current={phase.slug === current ? 'page' : undefined}
			class="flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1.5 font-medium transition-colors {phase.slug ===
			current
				? 'bg-accent/15 text-accent'
				: 'text-muted hover:text-ink'}"
		>
			<span class="font-mono text-[10px] opacity-70">{phase.id}</span>
			{phase.name}
		</a>
	{/each}
</nav>
