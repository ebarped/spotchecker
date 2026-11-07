<script lang="ts">
	import { useSvelteFlow } from '@xyflow/svelte';

	/** Re-fits the view whenever the trigger changes (a new turn, a new tool). */
	let { trigger = 0 }: { trigger?: number } = $props();

	const { fitView } = useSvelteFlow();

	$effect(() => {
		void trigger;
		// A tick lets Svelte Flow measure the freshly rendered nodes first.
		setTimeout(() => void fitView({ padding: 0.2, duration: 300 }), 50);
	});
</script>
