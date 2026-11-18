<script lang="ts">
	import { onMount } from 'svelte';
	import { agent } from '../lib/agent.svelte';
	import AgentView from '../lib/components/agent/AgentView.svelte';
	import CategoryStatusChart from '../lib/components/CategoryStatusChart.svelte';
	import Header from '../lib/components/Header.svelte';
	import MunicipalityChart from '../lib/components/MunicipalityChart.svelte';
	import RiskHistogram from '../lib/components/RiskHistogram.svelte';
	import Select from '../lib/components/Select.svelte';
	import StatCards from '../lib/components/StatCards.svelte';
	import TierDonut from '../lib/components/TierDonut.svelte';
	import { toasts } from '../lib/toast.svelte';
	import {
		CATEGORY_LABELS,
		filterQueue,
		STATUS_META,
		STATUSES,
		TIERS,
		TIER_COLORS,
		TIER_LABELS,
		judgeRiskText,
		riskText,
		scanAge,
		scoreChanges,
		type Category,
		type SuggestedStatus,
		type Tier,
		type TurislabReport
	} from '../lib/turislab';

	let report = $state<TurislabReport | null>(null);
	let phase = $state<'loading' | 'ready' | 'error'>('loading');
	let errorMessage = $state('');

	let tier = $state<Tier | ''>('');
	let category = $state<Category | ''>('');
	let status = $state<SuggestedStatus | ''>('');
	let q = $state('');
	let busy = $state(false);
	/** Establishments with a re-scan in flight, waiting for their new score. */
	let pending = $state<Set<string>>(new Set());

	// The open tab survives reloads, like the theme.
	const TAB_KEY = 'spotchecker:tab';
	let tab = $state<'report' | 'agent'>(
		typeof localStorage !== 'undefined' && localStorage.getItem(TAB_KEY) === 'agent'
			? 'agent'
			: 'report'
	);

	const filtered = $derived(report ? filterQueue(report.queue, { tier, category, status, q }) : []);

	// The first establishment without a score is the scanner's next target:
	// the waiting tail is ordered by the upcoming scan order.
	const nextId = $derived(report?.queue.find((i) => !i.score)?.establishment.id ?? null);

	const categories: Category[] = ['restaurant', 'hotel', 'bar', 'other'];

	function setTab(next: 'report' | 'agent') {
		tab = next;
		try {
			localStorage.setItem(TAB_KEY, next);
		} catch {
			// storage unavailable: the tab just does not persist
		}
	}

	/**
	 * Swaps the report in and toasts what changed against the previous
	 * snapshot: finished scans, scan errors and the establishments the
	 * scanner re-scored (a changed computed_at), first scans included.
	 */
	function applyReport(next: TurislabReport) {
		const changes = scoreChanges(report, next);
		if (report?.scan_running && !next.scan_running && !next.scan_error) {
			toasts.ok('Scan terminado', `${next.summary.total} establecimientos en la cola`);
		}
		if (next.scan_error && next.scan_error !== report?.scan_error) {
			toasts.error('El scan terminó con error', next.scan_error);
		}
		for (const change of changes.slice(0, 3)) {
			toasts.ok(
				change.first ? `${change.name}: primer score` : `${change.name} sincronizado`,
				`riesgo de cierre ${Math.round(change.closureRisk * 100)}% · tier ${change.tier}`
			);
		}
		if (changes.length > 3) {
			toasts.info('Sincronización en curso', `${changes.length - 3} establecimientos más actualizados`);
		}
		if (changes.length > 0) {
			pending = new Set([...pending].filter((id) => !changes.some((c) => c.id === id)));
		}
		report = next;
		phase = 'ready';
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
			applyReport((await res.json()) as TurislabReport);
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : String(err);
			phase = 'error';
			// Only the first load and manual reloads toast: the 5s polling
			// loop must not stack errors while the server is down.
			if (attempt === 0) toasts.error('No se pudo leer el informe', errorMessage);
		} finally {
			if (attempt === 0) busy = false;
		}
	}

	async function refresh(id: string, name: string) {
		toasts.info(`Sincronizando ${name}…`, 'El re-escaneo corre en segundo plano');
		try {
			const res = await fetch(`/api/establishments/${id}/refresh`, { method: 'POST' });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			// A single-establishment refresh does not flip the scanner's
			// running flag, so the page polls until its score shows up in
			// the report (or gives up after 90 s if the scan stalls).
			pending = new Set(pending).add(id);
			setTimeout(
				() => {
					pending = new Set([...pending].filter((x) => x !== id));
				},
				90_000
			);
		} catch (err) {
			toasts.error(`No se pudo sincronizar ${name}`, err instanceof Error ? err.message : String(err));
		}
	}

	function reload() {
		void load();
	}

	onMount(() => {
		void load();
		void agent.loadInfo();
		// Poll while a scan runs or a re-scan is in flight so the queue and
		// its toasts update themselves; idle ticks are a no-op.
		const timer = setInterval(() => {
			if (report?.scan_running || pending.size > 0) void load(1);
		}, 3000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>SpotChecker · Cola de inspección</title>
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
				Spot<span class="aurora-text">Checker</span>
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
				<div class="glass inline-flex items-center gap-2 rounded-full px-4 py-1.5 text-sm">
					<svg class="size-4 shrink-0 text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" /></svg>
					<input
						type="search"
						bind:value={q}
						placeholder="Buscar por nombre o ID…"
						class="w-48 bg-transparent text-sm outline-none placeholder:text-faint sm:w-64"
					/>
				</div>
				<Select
					label="Tier"
					icon="M22 3H2l8 9v7l4 2v-9l8-9z"
					hideLabel
					bind:value={tier}
					options={[{ value: '', label: 'Todos los tiers' }, ...TIERS.map((t) => ({ value: t, label: TIER_LABELS[t], color: TIER_COLORS[t] }))]}
				/>
				<Select
					label="Categoría"
					icon="M4 6h16M4 12h16M4 18h16"
					hideLabel
					bind:value={category}
					options={[{ value: '', label: 'Todas las categorías' }, ...categories.map((c) => {
						const icons: Record<string, string> = {
							restaurant: 'M3 2v7c0 1.1.9 2 2 2h4c1.1 0 2-.9 2-2V2M7 2v20M21 15V2a5 5 0 0 0-5 5v6c0 1.1.9 2 2 2h3zm0 0v7',
							hotel: 'M2 4v16M2 8h18a2 2 0 0 1 2 2v10M2 17h20M6 8v-2a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2',
							bar: 'M8 22h8M12 11v11M19 3H5l7 8 7-8z',
							other: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM17 17m-3 0a3 3 0 1 0 6 0a3 3 0 1 0-6 0'
						};
						return { value: c, label: CATEGORY_LABELS[c], icon: icons[c] };
					})]}
				/>
				<Select
					label="Estado"
					icon="M12 22a10 10 0 100-20 10 10 0 000 20z"
					hideLabel
					bind:value={status}
					options={[{ value: '', label: 'Todos los estados' }, ...STATUSES.map((s) => ({ value: s, label: STATUS_META[s].label, color: STATUS_META[s].color }))]}
				/>
			</div>

			<!-- Queue -->
			<div class="glass overflow-x-auto rounded-2xl">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b border-line text-left text-[11px] uppercase tracking-wider text-muted">
							<th class="px-4 py-3 font-semibold">ID</th>
							<th class="px-4 py-3 font-semibold">Establecimiento</th>
							<th class="px-4 py-3 font-semibold">Tier</th>
							<th class="px-4 py-3 font-semibold">Estado</th>
							<th class="px-4 py-3 font-semibold">Riesgo</th>
							<th class="px-4 py-3 font-semibold">Motivo</th>
							<th class="px-4 py-3 font-semibold">Scan</th>
							<th class="px-4 py-3"></th>
						</tr>
					</thead>
					<tbody>
						{#each filtered as item (item.establishment.id)}
							<tr class="border-b border-line/60 last:border-0">
								<td class="px-4 py-3 whitespace-nowrap">
									<a
										href={`/detalle?id=${encodeURIComponent(item.establishment.id)}`}
										class="mono text-xs text-accent hover:underline whitespace-nowrap"
										title={`Ver la ficha de ${item.establishment.name}`}
									>
										{item.establishment.id}
									</a>
								</td>
								<td class="px-4 py-3">
									<a
										href={`/detalle?id=${encodeURIComponent(item.establishment.id)}`}
										class="font-medium hover:text-accent"
									>
										{item.establishment.name}
									</a>
									<p class="text-xs text-muted">
										{CATEGORY_LABELS[item.establishment.category]} · {item.establishment
											.municipality}
									</p>
								</td>
								<td class="px-4 py-3">
									{#if item.score}
										<span
											class="inline-flex items-center justify-center rounded-full px-2.5 py-0.5 text-center text-xs font-semibold text-white"
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
								<td class="px-4 py-3">
									{#if item.score}
										<span
											class="inline-flex items-center justify-center rounded-full px-2.5 py-0.5 text-center text-xs font-semibold text-white"
											style="background: {STATUS_META[item.score.status].color}"
										>
											{STATUS_META[item.score.status].label}
										</span>
									{:else}
										<span class="text-xs text-faint">—</span>
									{/if}
								</td>
								<td class="px-4 py-3 font-mono text-muted">
									{item.score ? judgeRiskText(item.score) : '—'}
								</td>
								<td class="max-w-xs px-4 py-3 text-xs text-muted">
									{#if item.score}
										{item.score.reason}
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
									{:else if item.establishment.id === nextId}
										<span class="font-medium text-accent">Siguiente en el scan</span>
									{:else}
										<span class="text-partial">Esperando en la cola</span>
									{/if}
								</td>
								<td class="px-4 py-3 text-right">
									<button
										type="button"
										onclick={() => void refresh(item.establishment.id, item.establishment.name)}
										class="glass glass-hover inline-flex items-center gap-1 rounded-full px-3 py-1 text-xs whitespace-nowrap"
										title="Re-escanear este establecimiento ahora"
									>
										<svg class="size-3.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
											<polyline points="23 4 23 10 17 10" />
											<path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
										</svg>
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
