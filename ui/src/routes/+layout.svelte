<script lang="ts">
	import '../app.css';
	import logo from '../lib/assets/logo.png';
	import AuroraBackground from '../lib/components/AuroraBackground.svelte';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
</script>

<svelte:head>
	<title>Turislab Doctor</title>
	<link rel="icon" type="image/png" href={logo} />
</svelte:head>

<AuroraBackground />

<div class="relative flex min-h-screen flex-col">
	{@render children()}
</div>
