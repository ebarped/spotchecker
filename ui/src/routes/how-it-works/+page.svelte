<script lang="ts">
	import { onMount } from 'svelte';
	import ThemeToggle from '../../lib/components/ThemeToggle.svelte';
	import type { Pipeline } from '../../lib/turislab';

	let pipeline = $state<Pipeline | null>(null);
	let phase = $state<'loading' | 'ready' | 'error'>('loading');
	let errorMessage = $state('');

	onMount(async () => {
		try {
			const res = await fetch('/api/pipeline');
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			pipeline = await res.json();
			phase = 'ready';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : String(err);
			phase = 'error';
		}
	});
</script>

<svelte:head>
	<title>TurisLab Doctor · Cómo funciona</title>
</svelte:head>

<div class="mx-auto w-full max-w-4xl px-4 py-10 sm:px-6">
	<header class="mb-8">
		<h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">Cómo funciona</h1>
		<p class="mt-2 max-w-2xl text-sm text-muted">
			El pipeline completo, de los datos en bruto a la cola priorizada de inspecciones. Seis
			fases, cada una con su artefacto.
		</p>
		<nav class="mt-3 text-xs">
			<a href="/turislab" class="glass glass-hover rounded-full px-3 py-1 text-muted">
				← Volver a la cola
			</a>
		</nav>
	</header>

	{#if phase === 'loading'}
		<p class="text-sm text-muted">Cargando el pipeline…</p>
	{:else if phase === 'error'}
		<div class="glass rounded-2xl p-6 text-sm">
			<p class="font-medium text-error">No se pudo cargar el pipeline</p>
			<p class="mt-1 text-muted">{errorMessage}</p>
		</div>
	{:else if pipeline}
		<ol class="relative space-y-0">
			{#each pipeline.phases as p, i (p.id)}
				<li class="relative flex gap-4 pb-8 last:pb-0">
					<!-- Connector line between phases -->
					{#if i < pipeline.phases.length - 1}
						<div
							class="absolute top-12 bottom-0 left-6 w-px bg-line"
							aria-hidden="true"
						></div>
					{/if}
					<div
						class="glass relative z-10 flex size-12 shrink-0 items-center justify-center rounded-full text-lg font-bold"
						style="color: var(--gd-accent)"
					>
						{p.id}
					</div>
					<div class="glass flex-1 rounded-2xl p-5">
						<div class="flex flex-wrap items-baseline justify-between gap-2">
							<h2 class="text-base font-semibold">{p.name}</h2>
							<span
								class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted"
							>
								{p.artifact}
							</span>
						</div>
						<p class="mt-2 text-sm text-muted">{p.what}</p>
					</div>
				</li>
			{/each}
		</ol>

		<div class="glass mt-8 rounded-2xl p-5 text-sm text-muted">
			<p class="font-medium text-ink">Qué hace y qué no hace el sistema</p>
			<ul class="mt-2 list-inside list-disc space-y-1">
				<li>Ordena los establecimientos por riesgo; no decide visitas ni cambia estados administrativos.</li>
				<li>Sugiere un estado (activo, cierre sospechado…) a partir de señales de fuentes públicas.</li>
				<li>La verificación final por teléfono es manual durante la PoC.</li>
				<li>Cada score lleva su motivo legible: siempre puedes ver por qué un establecimiento está arriba.</li>
			</ul>
		</div>
	{/if}
</div>
