<script lang="ts">
	import { onMount } from 'svelte';

	// The root URL has no report of its own: the scan report lives at /turislab.
	onMount(() => {
		location.replace('/turislab/');
	});
</script>

<a href="/turislab/" class="text-muted">Go to the Turislab report</a>
