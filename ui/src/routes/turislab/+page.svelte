<script lang="ts">
	import { onMount } from 'svelte';

	// Kept for old bookmarks: the report now lives at the root. The Go SPA
	// handler serves this page for /turislab and the client jumps to /.
	onMount(() => {
		location.replace('/');
	});
</script>

<a href="/" class="text-muted">Ir al informe de SpotChecker</a>
