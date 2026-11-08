<script lang="ts">
	import { onMount } from 'svelte';
	import ThemeToggle from '../../lib/components/ThemeToggle.svelte';
	import {
		CATEGORY_LABELS,
		filterQueue,
		STATUS_LABELS,
		TIERS,
		TIER_COLORS,
		TIER_LABELS,
		riskLabel,
		scanAge,
		type Category,
		type TurislabReport,
		type Tier
	} from '../../lib/turislab';

	let report = $state<TurislabReport | null>(null);
	let phase = $state<'loading' | 'ready' | 'error'>('loading');
	let errorMessage = $state('');

	let tier = $state<Tier | ''>('');
	let category = $state<Category | ''>('');
	let q = $state('');

	const filtered = $derived(report ? filterQueue(report.queue, { tier, category, q }) : []);

	const categories: Category[] = ['restaurant', 'hotel', 'bar', 'other'];

	async function load(attempt = 0) {
		if (attempt === 0) phase = 'loading';
		try {
			const res = await fetch('/api/report');
			if (res.status === 503) {
				// The scanner has not produced a report yet; retry shortly.
				if (attempt < 20) {
					setTimeout(() => void load(attempt + 1), 2000);
					return;
				}
				errorMessage = 'El scan todavía no ha generado ningún informe. Espera al primer ciclo.';
				phase = 'error';
				return;
			}
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			report = await res.json();
			phase = 'ready';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : String(err);
			phase = 'error';
		}
	}

	async function refresh(id: string) {
		await fetch(`/api/establishments/${id}/refresh`, { method: 'POST' });
	}

	function reload() {
		void load();
	}

	onMount(() => {
		void load();
		// Poll while a scan is running so the queue updates itself.
		const timer = setInterval(() => {
			if (report?.scan_running) void load(1);
		}, 5000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>TurisLab Doctor · Cola de inspección</title>
</svelte:head>

<div class="mx-auto w-full max-w-6xl px-4 py-10 sm:px-6">
	<header class="mb-8 flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
		<div>
			<div class="flex items-center gap-4">
				<h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">TurisLab Doctor</h1>
			</div>
			<p class="mt-2 max-w-2xl text-sm text-muted">
				Cola priorizada de establecimientos con riesgo de cierre o pérdida de calidad. El
				sistema solo ordena: la decisión de visita es del inspector.
			</p>
			<nav class="mt-3 flex items-center gap-2 text-xs">
				<span class="glass inline-flex items-center gap-1.5 rounded-full px-3 py-1">
					<span
						class="size-1.5 rounded-full"
						style="background: {report?.scan_running ? 'var(--color-partial)' : 'var(--color-ok)'}"
					></span>
					{report?.scan_running ? 'scan en curso' : 'scan en reposo'}
				</span>
				<a href="/how-it-works" class="glass glass-hover rounded-full px-3 py-1 text-muted">
					Cómo funciona →
				</a>
			</nav>
		</div>
		<div class="flex items-center gap-2">
			<button
				type="button"
				onclick={reload}
				class="glass glass-hover flex size-9 items-center justify-center rounded-full text-muted"
				aria-label="Recargar la cola"
				title="Recargar la cola"
			>
				<svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
					<path stroke-linecap="round" stroke-linejoin="round" d="M21 12a9 9 0 1 1-3-6.7" />
					<polyline points="21 3 21 9 15 9" />
				</svg>
			</button>
			<ThemeToggle />
		</div>
	</header>

	{#if phase === 'loading'}
		<p class="text-sm text-muted">Cargando la cola…</p>
	{:else if phase === 'error'}
		<div class="glass rounded-2xl p-6 text-sm">
			<p class="font-medium text-error">No se pudo cargar el informe</p>
			<p class="mt-1 text-muted">{errorMessage}</p>
		</div>
	{:else if report}
		<!-- Summary strip -->
		<div class="mb-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
			{#each TIERS as t (t)}
				<div class="glass rounded-2xl p-4">
					<p class="text-[11px] font-semibold tracking-wider uppercase text-muted">
						{TIER_LABELS[t]}
					</p>
					<p class="mt-1 text-3xl font-bold tracking-tight">
						{t === 'P0'
							? report.summary.p0
							: t === 'P1'
								? report.summary.p1
								: t === 'P2'
									? report.summary.p2
									: t === 'P3'
										? report.summary.p3
										: report.summary.collect_data}
					</p>
				</div>
			{/each}
		</div>

		<!-- Filters -->
		<div class="mb-4 flex flex-wrap items-center gap-2">
			<input
				type="search"
				bind:value={q}
				placeholder="Buscar por nombre…"
				class="glass rounded-full px-4 py-1.5 text-sm outline-none placeholder:text-faint"
			/>
			<select bind:value={tier} class="glass rounded-full px-3 py-1.5 text-sm">
				<option value="">Todos los tiers</option>
				{#each TIERS as t (t)}
					<option value={t}>{TIER_LABELS[t]}</option>
				{/each}
			</select>
			<select bind:value={category} class="glass rounded-full px-3 py-1.5 text-sm">
				<option value="">Todas las categorías</option>
				{#each categories as c (c)}
					<option value={c}>{CATEGORY_LABELS[c]}</option>
				{/each}
			</select>
			<span class="ml-auto text-xs text-faint">
				{filtered.length} de {report.queue.length} establecimientos ·
				cobertura {Math.round(report.summary.coverage_pct)}%
			</span>
		</div>

		<!-- Queue -->
		<div class="glass overflow-hidden rounded-2xl">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-line text-left text-[11px] uppercase tracking-wider text-muted">
						<th class="px-4 py-3 font-semibold">#</th>
						<th class="px-4 py-3 font-semibold">Establecimiento</th>
						<th class="px-4 py-3 font-semibold">Tier</th>
						<th class="px-4 py-3 font-semibold">Riesgo cierre</th>
						<th class="px-4 py-3 font-semibold">Prioridad</th>
						<th class="px-4 py-3 font-semibold">Motivo</th>
						<th class="px-4 py-3 font-semibold">Scan</th>
						<th class="px-4 py-3"></th>
					</tr>
				</thead>
				<tbody>
					{#each filtered as item (item.establishment.id)}
						<tr class="border-b border-line/60 last:border-0">
							<td class="px-4 py-3 font-mono text-muted">{item.rank}</td>
							<td class="px-4 py-3">
								<p class="font-medium">{item.establishment.name}</p>
								<p class="text-xs text-muted">
									{CATEGORY_LABELS[item.establishment.category]} · {item.establishment.municipality}
								</p>
							</td>
							<td class="px-4 py-3">
								<span
									class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold text-white"
									style="background: {TIER_COLORS[item.score.tier]}"
								>
									{TIER_LABELS[item.score.tier]}
								</span>
							</td>
							<td class="px-4 py-3 font-mono">{riskLabel(item.score.closure_risk)}</td>
							<td class="px-4 py-3 font-mono">{Math.round(item.score.priority)}</td>
							<td class="max-w-xs px-4 py-3 text-xs text-muted">
								{STATUS_LABELS[item.score.status]} — {item.score.reason}
								{#if item.score.conflict_detected}
									<span class="text-partial">· conflicto entre fuentes</span>
								{/if}
							</td>
							<td class="px-4 py-3 text-xs text-muted">{scanAge(item.scan_age_ns)}</td>
							<td class="px-4 py-3 text-right">
								<button
									type="button"
									onclick={() => void refresh(item.establishment.id)}
									class="glass glass-hover rounded-full px-3 py-1 text-xs"
									title="Re-escanear este establecimiento ahora"
								>
									Re-escanear
								</button>
							</td>
						</tr>
					{:else}
						<tr>
							<td colspan="8" class="px-4 py-8 text-center text-sm text-muted">
								Ningún establecimiento coincide con los filtros.
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		{#if report.scan_error}
			<p class="mt-4 text-xs text-error">Último error del scan: {report.scan_error}</p>
		{/if}
	{/if}
</div>
