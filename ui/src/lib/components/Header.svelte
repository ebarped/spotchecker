<script lang="ts">
	import logo from '../assets/logo.png';
	import { formatTime, type TurislabReport } from '../turislab';
	import ThemeToggle from './ThemeToggle.svelte';

	let {
		report,
		onreload,
		busy = false
	}: { report: TurislabReport; onreload: () => void; busy?: boolean } = $props();

	const enabledSources = $derived(report.sources.filter((source) => source.enabled).length);
</script>

<header class="mb-8 flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
	<div class="min-w-0">
		<div class="flex items-center gap-4">
			<img src={logo} alt="Gopher con estetoscopio" class="size-12 shrink-0 drop-shadow-sm sm:size-14" />
			<h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">TurisLab Doctor</h1>
		</div>
		<p class="mt-2 max-w-2xl text-sm text-muted">
			Cola priorizada de establecimientos con riesgo de cierre o pérdida de calidad. El sistema
			solo ordena: la decisión de visita es del inspector.
		</p>

		<div class="mt-3 flex flex-wrap items-center gap-2 text-xs">
			<span class="glass inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-muted">
				<span
					class="size-1.5 rounded-full"
					style="background: {report.scan_running ? 'var(--color-partial)' : 'var(--color-ok)'}"
				></span>
				{report.scan_running ? 'scan en curso' : 'scan en reposo'}
			</span>
			<span class="glass inline-flex items-center rounded-full px-3 py-1 text-muted">
				cobertura {Math.round(report.summary.coverage_pct)}%
			</span>
			<span class="glass inline-flex items-center rounded-full px-3 py-1 text-muted">
				{enabledSources} {enabledSources === 1 ? 'fuente activa' : 'fuentes activas'}
			</span>
			<span class="glass inline-flex items-center rounded-full px-3 py-1 text-faint">
				generado {formatTime(report.generated_at)}
			</span>
			<a href="/how-it-works" class="glass glass-hover inline-flex items-center gap-1 rounded-full px-3 py-1 text-muted">
				Cómo funciona
				<svg class="size-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M7 17 17 7M9 7h8v8" />
				</svg>
			</a>
		</div>
	</div>

	<div class="flex items-center gap-2">
		<a
			href="/api/report/export"
			aria-label="Descargar el informe en JSON"
			title="Descargar el informe en JSON"
			class="glass glass-hover flex size-9 items-center justify-center rounded-full text-muted"
		>
			<svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
				<path
					stroke-linecap="round"
					stroke-linejoin="round"
					d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"
				/>
				<polyline points="7 10 12 15 17 10" stroke-linecap="round" stroke-linejoin="round" />
				<line x1="12" y1="15" x2="12" y2="3" stroke-linecap="round" />
			</svg>
		</a>
		<button
			type="button"
			onclick={onreload}
			disabled={busy}
			aria-label={busy ? 'Recargando la cola' : 'Recargar la cola'}
			title={busy ? 'Recargando…' : 'Recargar la cola'}
			class="glass glass-hover flex size-9 items-center justify-center rounded-full text-muted disabled:opacity-60"
		>
			<svg
				class="size-4 {busy ? 'animate-spin' : ''}"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="1.8"
			>
				<polyline points="23 4 23 10 17 10" stroke-linecap="round" stroke-linejoin="round" />
				<path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" stroke-linecap="round" stroke-linejoin="round" />
			</svg>
		</button>
		<ThemeToggle />
	</div>
</header>
