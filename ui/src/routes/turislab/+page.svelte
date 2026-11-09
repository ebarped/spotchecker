<script lang="ts">
	import { onMount } from 'svelte';
	import { agent } from '../../lib/agent.svelte';
	import AgentView from '../../lib/components/agent/AgentView.svelte';
	import CategoryStatusChart from '../../lib/components/CategoryStatusChart.svelte';
	import Header from '../../lib/components/Header.svelte';
	import MunicipalityChart from '../../lib/components/MunicipalityChart.svelte';
	import RiskHistogram from '../../lib/components/RiskHistogram.svelte';
	import StatCards from '../../lib/components/StatCards.svelte';
	import TierDonut from '../../lib/components/TierDonut.svelte';
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
		type Tier,
		type TurislabReport
	} from '../../lib/turislab';

	let report = $state<TurislabReport | null>(null);
	let phase = $state<'loading' | 'ready' | 'error'>('loading');
	let errorMessage = $state('');

	let tier = $state<Tier | ''>('');
	let category = $state<Category | ''>('');
	let q = $state('');
	let busy = $state(false);

	// The open tab survives reloads, like the theme.
	const TAB_KEY = 'turislab-doctor:tab';
	let tab = $state<'report' | 'agent'>(
		typeof localStorage !== 'undefined' && localStorage.getItem(TAB_KEY) === 'agent'
			? 'agent'
			: 'report'
	);

	const filtered = $derived(report ? filterQueue(report.queue, { tier, category, q }) : []);

	const categories: Category[] = ['restaurant', 'hotel', 'bar', 'other'];

	function setTab(next: 'report' | 'agent') {
		tab = next;
		try {
			localStorage.setItem(TAB_KEY, next);
		} catch {
			// storage unavailable: the tab just does not persist
		}
	}

	async function load(attempt = 0) {
		if (attempt === 0) {
			phase = 'loading';
			busy = true;
		}
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
			const type = res.headers.get('content-type') ?? '';
			if (!type.includes('json')) {
				throw new Error(`/api/report respondió ${type || 'sin content-type'} en vez de JSON`);
			}
			report = (await res.json()) as TurislabReport;
			phase = 'ready';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : String(err);
			phase = 'error';
		} finally {
			if (attempt === 0) busy = false;
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
		void agent.loadInfo();
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

<div class="mx-auto w-full max-w-7xl flex-1 px-4 pt-8 pb-16 sm:px-6 lg:px-8">
	{#if phase === 'loading' && !report}
		<div class="animate-pulse space-y-6" aria-hidden="true">
			<div class="glass h-28 rounded-2xl"></div>
			<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
				{#each [1, 2, 3, 4] as cell (cell)}
					<div class="glass h-36 rounded-2xl"></div>
				{/each}
			</div>
			<div class="glass h-96 rounded-2xl"></div>
		</div>
		<span class="sr-only">Cargando el informe…</span>
	{:else if phase === 'error' && !report}
		<section class="glass relative mx-auto mt-12 max-w-2xl rounded-2xl p-8">
			<h1 class="text-2xl font-semibold tracking-tight">
				TurisLab<span class="aurora-text"> Doctor</span>
			</h1>
			<p class="mt-4 text-sm text-muted">No se pudo leer ningún informe.</p>
			<p class="mono mt-1 text-xs text-error">{errorMessage}</p>
			<button
				type="button"
				onclick={() => void load()}
				class="mt-4 rounded-xl border border-line bg-surface-2 px-4 py-2 text-sm transition-colors hover:border-line-strong"
			>
				Reintentar
			</button>
		</section>
	{:else if report}
		<Header {report} onreload={reload} {busy} />

		{#if report.scan_error}
			<div class="glass mb-6 rounded-xl border-error/30 px-4 py-3 text-sm text-error">
				El último scan falló: {report.scan_error} — la cola muestra el estado anterior.
			</div>
		{/if}

		<div class="mb-6 flex justify-center">
			<div class="glass inline-flex rounded-full p-1">
				<button
					type="button"
					class="rounded-full px-4 py-1.5 text-sm font-medium transition-colors {tab === 'report'
						? 'bg-accent/15 text-accent'
						: 'text-muted hover:text-ink'}"
					onclick={() => setTab('report')}
				>
					Informe
				</button>
				<button
					type="button"
					class="rounded-full px-4 py-1.5 text-sm font-medium transition-colors {tab === 'agent'
						? 'bg-accent/15 text-accent'
						: 'text-muted hover:text-ink'}"
					onclick={() => {
						setTab('agent');
						void agent.loadCatalog();
					}}
				>
					Agente
					{#if agent.info !== null && !agent.info.enabled}
						<span class="ml-1.5 text-[10px] font-semibold uppercase tracking-wider text-faint">
							off
						</span>
					{/if}
				</button>
			</div>
		</div>

		{#if tab === 'agent'}
			{#if agent.info?.enabled}
				<AgentView ready={phase === 'ready'} />
			{:else}
				<section class="glass mx-auto max-w-2xl rounded-2xl p-8 text-center">
					<h2 class="text-lg font-semibold">El agente no está habilitado</h2>
					<p class="mt-2 text-sm text-muted">
						Este servidor sirve el informe y la cola, pero no tiene montado el asistente. Para
						activarlo, pon <code class="mono text-xs">agent.enabled: true</code> en la configuración y
						define la clave del modelo en la variable que indique
						<code class="mono text-xs">agent.model.api_key_env</code>.
					</p>
				</section>
			{/if}
		{:else}
			<StatCards {report} />

			<div class="mt-4 grid gap-4 lg:grid-cols-3">
				<section class="glass rounded-2xl p-5 lg:col-span-1">
					<h2 class="text-sm font-semibold">Distribución por tier</h2>
					<p class="mb-3 text-xs text-faint">Toda la cola priorizada por urgencia</p>
					<TierDonut {report} />
				</section>

				<section class="glass rounded-2xl p-5 lg:col-span-2">
					<h2 class="text-sm font-semibold">Establecimientos por municipio</h2>
					<p class="mb-3 text-xs text-faint">
						Los municipios con más establecimientos en la cola, partidos por tier
					</p>
					<MunicipalityChart {report} />
				</section>
			</div>

			<div class="mt-4 grid gap-4 lg:grid-cols-2">
				<section class="glass rounded-2xl p-5">
					<h2 class="text-sm font-semibold">Estado por categoría</h2>
					<p class="mb-3 text-xs text-faint">Qué tipo de negocio hay detrás de cada estado</p>
					<CategoryStatusChart {report} />
				</section>

				<section class="glass rounded-2xl p-5">
					<h2 class="text-sm font-semibold">Riesgo de cierre</h2>
					<p class="mb-3 text-xs text-faint">
						Establecimientos por tramo de riesgo calculado
					</p>
					<RiskHistogram {report} />
				</section>
			</div>

			<!-- Filters -->
			<div class="mt-8 mb-4 flex flex-wrap items-center gap-2">
				<h2 class="mr-2 text-sm font-semibold">Cola de inspección</h2>
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
										{CATEGORY_LABELS[item.establishment.category]} · {item.establishment
											.municipality}
									</p>
								</td>
								<td class="px-4 py-3">
									{#if item.score}
										<span
											class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold text-white"
											style="background: {TIER_COLORS[item.score.tier]}"
										>
											{TIER_LABELS[item.score.tier]}
										</span>
									{:else}
										<span
											class="inline-flex items-center rounded-full bg-surface-2 px-2.5 py-0.5 text-xs font-medium text-muted"
										>
											En cola
										</span>
									{/if}
								</td>
								<td class="px-4 py-3 font-mono">
									{item.score ? riskLabel(item.score.closure_risk) : '—'}
								</td>
								<td class="px-4 py-3 font-mono">
									{item.score ? Math.round(item.score.priority) : '—'}
								</td>
								<td class="max-w-xs px-4 py-3 text-xs text-muted">
									{#if item.score}
										{STATUS_LABELS[item.score.status]} — {item.score.reason}
										{#if item.score.conflict_detected}
											<span class="text-partial">· conflicto entre fuentes</span>
										{/if}
									{:else}
										Sin datos todavía
									{/if}
								</td>
								<td class="px-4 py-3 text-xs text-muted">
									{#if item.score}
										{scanAge(item.scan_age_ns)}
									{:else}
										<span class="text-partial">Esperando en la cola</span>
									{/if}
								</td>
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

			<footer class="mt-8 text-center text-xs text-faint">
				Mostrando el informe en vivo del servidor · generado {new Date(
					report.generated_at
				).toLocaleString('es-ES')}
			</footer>
		{/if}
	{/if}
</div>
