<script lang="ts">
	import { theme } from '../theme.svelte';
</script>

<button
	type="button"
	onclick={() => theme.toggle()}
	aria-label="Switch to the {theme.theme === 'dark' ? 'light' : 'dark'} aurora"
	title="Switch to the {theme.theme === 'dark' ? 'light' : 'dark'} aurora"
	class="glass glass-hover flex size-9 items-center justify-center rounded-full text-muted"
>
	{#if theme.theme === 'dark'}
		<!-- sun -->
		<svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
			<circle cx="12" cy="12" r="4" />
			<path
				stroke-linecap="round"
				d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8"
			/>
		</svg>
	{:else}
		<!-- moon -->
		<svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
			<path
				stroke-linecap="round"
				stroke-linejoin="round"
				d="M20.5 14.5A8.5 8.5 0 1 1 9.5 3.5a7 7 0 0 0 11 11Z"
			/>
		</svg>
	{/if}
</button>
