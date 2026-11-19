<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { toasts } from '../../lib/toast.svelte';
	import {
		CATEGORY_LABELS,
		formatCoord,
		formatDate,
		formatTime,
		mapsEmbedURL,
		mapsURL,
		pct,
		judgeRiskText,
		riskLabel,
		riskText,
		scanAge,
		STATUS_META,
		TIER_COLORS,
		TIER_LABELS,
		type QueueItem
	} from '../../lib/turislab';

	// The id travels in the query string (/detalle?id=R-LU-000710): a static
	// route needs no dynamic segment, so the prerendered build and the Go
	// static server keep working unchanged.
	const id = $derived(page.url.searchParams.get('id') ?? '');

	let item = $state<QueueItem | null>(null);
	/** Website capture saved by the browser MCP, if one exists for this id. */
	let screenshot = $state<{
		url: string;
		capturedAt: string | null;
		lastModified: string | null;
	} | null>(null);
	let phase = $state<'loading' | 'ready' | 'missing' | 'error'>('loading');
	let errorMessage = $state('');
	let pending = $state(false);
	/** computed_at at the moment of the re-scan request; plain, not reactive. */
	let pendingComputedAt: string | null = null;

	const est = $derived(item?.establishment ?? null);
	const score = $derived(item?.score ?? null);
	const evidence = $derived(
		[...(item?.primary_signals ?? [])].sort((a, b) => b.effective_weight - a.effective_weight)
	);
	const riskPct = $derived(score && score.risk_known ? Math.round(score.closure_risk * 100) : 0);
	const riskColor = $derived(score ? STATUS_META[score.status].color : 'var(--color-faint)');
	// The judge's own risk is only meaningful when it actually judged.
	const judgeRiskPct = $derived(
		score && score.judge_status && score.judge_risk_known
			? Math.round(Math.min(Math.max(score.judge_risk ?? 0, 0), 1) * 100)
			: 0
	);
	const judgeRiskColor = $derived(
		score && score.judge_status ? STATUS_META[score.judge_status].color : 'var(--color-faint)'
	);
	/** The clickable website, normalised to an absolute http(s) URL. */
	const websiteURL = $derived.by(() => {
		const raw = (item?.website_url || est?.website)?.trim();
		if (!raw) return null;
		try {
			const url = new URL(raw.includes('://') ? raw : `https://${raw}`);
			return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : null;
		} catch {
			return null;
		}
	});

	function dash(value: string | null | undefined): string {
		return value && value.trim() !== '' ? value : '—';
	}

	/** The screenshot is a runtime file, so it is not part of the QueueItem. */
	async function checkScreenshot(establishmentID: string) {
		const endpoint = `/api/establishments/${encodeURIComponent(establishmentID)}/screenshot`;
		try {
			const res = await fetch(endpoint, { method: 'HEAD', cache: 'no-store' });
			if (!res.ok || id !== establishmentID) {
				screenshot = null;
				return;
			}
			const lastModified = res.headers.get('Last-Modified');
			// The page polls every 5s: only swap the image when the file changed,
			// otherwise the same photo would reload on every poll.
			if (screenshot && screenshot.lastModified === lastModified) return;
			screenshot = {
				url: `${endpoint}?t=${Date.now()}`,
				capturedAt: lastModified,
				lastModified
			};
		} catch {
			screenshot = null;
		}
	}

	async function load(establishmentID: string, attempt = 0) {
		try {
			const res = await fetch(`/api/establishments/${encodeURIComponent(establishmentID)}`);
			if (res.status === 404) {
				item = null;
				phase = 'missing';
				return;
			}
			if (res.status === 503) {
				// The scanner has not produced a report yet; retry shortly.
				if (attempt < 20) {
					setTimeout(() => void load(establishmentID, attempt + 1), 2000);
					return;
				}
				throw new Error('El scan todavía no ha generado ningún informe.');
			}
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const next = (await res.json()) as QueueItem;
			if (pendingComputedAt !== null && next.score && next.score.computed_at !== pendingComputedAt) {
				pendingComputedAt = null;
				pending = false;
				toasts.ok(
					`${next.establishment.name}: score actualizado`,
					`riesgo de cierre ${riskText(next.score)} · tier ${next.score.tier}`
				);
			}
			item = next;
			phase = 'ready';
			void checkScreenshot(establishmentID);
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : String(err);
			phase = 'error';
		}
	}

	async function rescan() {
		if (!item) return;
		const current = item;
		pending = true;
		pendingComputedAt = current.score?.computed_at ?? '';
		toasts.info(
			`Sincronizando ${current.establishment.name}…`,
			'El re-escaneo corre en segundo plano'
		);
		try {
			const res = await fetch(
				`/api/establishments/${encodeURIComponent(current.establishment.id)}/refresh`,
				{ method: 'POST' }
			);
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			// A scan that stalls must not leave the button spinning forever.
			setTimeout(() => {
				if (pendingComputedAt !== null) {
					pendingComputedAt = null;
					pending = false;
				}
			}, 90_000);
		} catch (err) {
			pending = false;
			pendingComputedAt = null;
			toasts.error('No se pudo re-escanear', err instanceof Error ? err.message : String(err));
		}
	}

	$effect(() => {
		const current = id;
		if (!current) {
			item = null;
			screenshot = null;
			phase = 'missing';
			return;
		}
		phase = 'loading';
		void load(current);
	});

	onMount(() => {
		// The page stays live while a scan runs elsewhere: the endpoint is
		// one row, so a quiet poll costs nothing.
		const timer = setInterval(() => {
			if (document.visibilityState !== 'visible' || !id) return;
			void load(id, 1);
		}, 5000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>{est ? `${est.name} · SpotChecker` : 'Ficha · SpotChecker'}</title>
</svelte:head>

<div class="mx-auto w-full max-w-6xl px-4 py-10 sm:px-6">
	<nav class="flex flex-wrap items-center gap-2 text-xs">
		<a href="/" class="glass glass-hover rounded-full px-3 py-1 text-muted">
			← Cola de inspección
		</a>
		<a href="/how-it-works" class="glass glass-hover rounded-full px-3 py-1 text-muted">
			Cómo funciona
		</a>
	</nav>

	{#if phase === 'loading' && !item}
		<div class="mt-6 animate-pulse space-y-4" aria-hidden="true">
			<div class="glass h-24 rounded-2xl"></div>
			<div class="grid gap-4 lg:grid-cols-3">
				<div class="glass h-96 rounded-2xl lg:col-span-2"></div>
				<div class="glass h-96 rounded-2xl"></div>
			</div>
		</div>
		<span class="sr-only">Cargando la ficha del establecimiento…</span>
	{:else if phase === 'missing'}
		<section class="glass mx-auto mt-10 max-w-2xl rounded-2xl p-8 text-center">
			<h1 class="text-2xl font-semibold">Ese establecimiento no existe</h1>
			<p class="mt-2 text-sm text-muted">
				El id <span class="mono text-ink">{id || '(vacío)'}</span> no está en el informe actual. Puede
				que el dataset haya cambiado: la carga es un upsert, así que un id retirado deja de aparecer
				tras regenerar la base de datos.
			</p>
			<a
				href="/"
				class="mt-4 inline-block rounded-xl border border-line bg-surface-2 px-4 py-2 text-sm transition-colors hover:border-line-strong"
			>
				Volver a la cola
			</a>
		</section>
	{:else if phase === 'error' && !item}
		<section class="glass mx-auto mt-10 max-w-2xl rounded-2xl p-8">
			<h1 class="text-2xl font-semibold">No se pudo leer la ficha</h1>
			<p class="mono mt-2 text-xs text-error">{errorMessage}</p>
			<button
				type="button"
				onclick={() => void load(id)}
				class="mt-4 rounded-xl border border-line bg-surface-2 px-4 py-2 text-sm transition-colors hover:border-line-strong"
			>
				Reintentar
			</button>
		</section>
	{:else if est}
		<header class="mt-5 flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
			<div class="min-w-0">
				<div class="flex flex-wrap items-center gap-2">
					<span class="glass rounded-full px-3 py-1 mono text-xs font-semibold text-accent">
						{est.registry_id || est.id}
					</span>
					<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted">
						{CATEGORY_LABELS[est.category]}
					</span>
					{#if est.stars != null}
						<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted">
							{est.stars}★
						</span>
					{/if}
					<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted">
						{dash(est.municipality)}{est.province ? ` · ${est.province}` : ''}
					</span>
				</div>
				<h1 class="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">{est.name}</h1>
				<div class="mt-3 flex flex-wrap items-center gap-2 text-xs">
					{#if score}
						<span
							class="inline-flex items-center justify-center rounded-full px-2.5 py-0.5 text-xs font-semibold text-white"
							style="background: {TIER_COLORS[score.tier]}"
						>
							{TIER_LABELS[score.tier]}
						</span>
						<span
							class="inline-flex items-center justify-center rounded-full px-2.5 py-0.5 text-xs font-semibold text-white"
							style="background: {STATUS_META[score.status].color}"
						>
							{STATUS_META[score.status].label}
						</span>
						<span class="glass rounded-full px-3 py-1 text-muted">
							scan {scanAge(item?.scan_age_ns ?? 0)}
						</span>
					{:else}
						<span class="rounded-full bg-surface-2 px-2.5 py-0.5 font-medium text-muted">
							Sin escanear
						</span>
					{/if}
				</div>
			</div>

			<div class="flex items-center gap-2">
				<button
					type="button"
					onclick={() => void rescan()}
					disabled={pending}
					class="glass glass-hover rounded-full px-4 py-2 text-sm text-muted disabled:opacity-60"
				>
					{pending ? 'Sincronizando…' : 'Re-escanear ahora'}
				</button>
			</div>
		</header>

		<div class="mt-6 grid gap-4 lg:grid-cols-3">
			<!-- Registry data: everything the dataset carries -->
			<section class="glass rounded-2xl p-5 lg:col-span-2">
				<h2 class="text-sm font-semibold">Ficha oficial</h2>
				<p class="mt-1 text-xs text-faint">Datos del registro tal y como llegan en el dataset.</p>

				<dl class="mt-4 grid gap-4 sm:grid-cols-2">
					<div>
						<dt class="caption">Identificador</dt>
						<dd class="mono mt-1 text-sm">{est.id}</dd>
					</div>
					<div>
						<dt class="caption">Categoría</dt>
						<dd class="mt-1 text-sm">
							{CATEGORY_LABELS[est.category]}{est.stars != null ? ` · ${est.stars}★` : ''}
						</dd>
					</div>
					<div>
						<dt class="caption">Dirección</dt>
						<dd class="mt-1 text-sm">{dash(est.address)}</dd>
					</div>
					<div>
						<dt class="caption">Código postal</dt>
						<dd class="mt-1 text-sm">{dash(est.postal_code)}</dd>
					</div>
					<div>
						<dt class="caption">Municipio</dt>
						<dd class="mt-1 text-sm">
							{dash(est.municipality)}{est.province ? ` · ${est.province}` : ''}
						</dd>
					</div>
					<div>
						<dt class="caption">País</dt>
						<dd class="mt-1 text-sm">{dash(est.country)}</dd>
					</div>
					<div>
						<dt class="caption">Teléfono</dt>
						<dd class="mt-1 text-sm">
							{#if est.phone}
								<a class="text-accent hover:underline" href={`tel:${est.phone}`}>{est.phone}</a>
							{:else}
								—
							{/if}
						</dd>
					</div>
					<div>
						<dt class="caption">Email</dt>
						<dd class="mt-1 truncate text-sm">
							{#if est.email}
								<a class="text-accent hover:underline" href={`mailto:${est.email}`}>{est.email}</a>
							{:else}
								—
							{/if}
						</dd>
					</div>
					<div>
						<dt class="caption">Web</dt>
						<dd class="mt-1 truncate text-sm">
							{#if websiteURL}
								<a
									class="text-accent hover:underline"
									href={websiteURL}
									target="_blank"
									rel="noreferrer">{est.website}</a
								>
							{:else}
								—
							{/if}
						</dd>
					</div>
					<div>
						<dt class="caption">Coordenadas</dt>
						<dd class="mono mt-1 text-sm">
							{#if est.lat != null && est.lng != null}
								{formatCoord(est.lat)}, {formatCoord(est.lng)}
							{:else}
								—
							{/if}
						</dd>
					</div>
					<div>
						<dt class="caption">Última inspección</dt>
						<dd class="mt-1 text-sm">
							{#if est.last_inspection_at}
								{formatDate(est.last_inspection_at)}{est.last_inspection_result
									? ` · ${est.last_inspection_result}`
									: ''}
							{:else}
								—
							{/if}
						</dd>
					</div>

					{#if est.certifications && est.certifications.length > 0}
						<div class="sm:col-span-2">
							<dt class="caption">Certificaciones</dt>
							<dd class="mt-1 flex flex-wrap gap-1.5">
								{#each est.certifications as cert (cert)}
									<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted">
										{cert}
									</span>
								{/each}
							</dd>
						</div>
					{/if}

					{#if est.google_place_id || est.osm_id || est.tripadvisor_id || est.yelp_id}
						<div class="sm:col-span-2">
							<dt class="caption">Identificadores externos</dt>
							<dd class="mono mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
								{#if est.google_place_id}<span>google: {est.google_place_id}</span>{/if}
								{#if est.osm_id}<span>osm: {est.osm_id}</span>{/if}
								{#if est.tripadvisor_id}<span>tripadvisor: {est.tripadvisor_id}</span>{/if}
								{#if est.yelp_id}<span>yelp: {est.yelp_id}</span>{/if}
							</dd>
						</div>
					{/if}

					{#if est.notes}
						<div class="sm:col-span-2">
							<dt class="caption">Notas</dt>
							<dd class="mt-1 text-sm text-muted">{est.notes}</dd>
						</div>
					{/if}

					{#if est.id_generated}
						<div class="rounded-xl bg-surface-2 p-3 text-xs text-partial sm:col-span-2">
							El identificador de este establecimiento se generó automáticamente: el dataset no
							traía uno.
						</div>
					{/if}
				</dl>

				<div class="mt-5 overflow-hidden rounded-xl border border-line">
					<div class="flex flex-wrap items-baseline justify-between gap-2 px-4 pt-4">
						<div>
							<h3 class="caption">Ubicación</h3>
							{#if est.lat == null || est.lng == null}
								<p class="mt-1 text-[11px] text-faint">
									Sin coordenadas en el registro: el mapa busca por nombre y dirección.
								</p>
							{/if}
						</div>
						<a
							class="text-[11px] text-accent hover:underline"
							href={mapsURL(est)}
							target="_blank"
							rel="noreferrer"
						>
							Abrir en Google Maps ↗
						</a>
					</div>
					<iframe
						class="mt-3 h-72 w-full border-0"
						src={mapsEmbedURL(est)}
						title={`Mapa de ${est.name}`}
						loading="lazy"
						referrerpolicy="no-referrer-when-downgrade"
						allowfullscreen
					></iframe>
				</div>
			</section>

			<!-- Score and judge -->
			<section class="glass rounded-2xl p-5">
				<h2 class="text-sm font-semibold">Score</h2>

				{#if score}
					<div class="mt-4 grid gap-3 sm:grid-cols-2">
						<div class="rounded-xl bg-surface-2 p-3">
							<div class="flex items-baseline justify-between">
								<span class="caption">Riesgo del motor</span>
								<span class="mono text-lg font-semibold">{riskText(score)}</span>
							</div>
							<div class="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-3">
								<div
									class="h-full rounded-full"
									style="width: {riskPct}%; background: {riskColor}"
								></div>
							</div>
						</div>
						<div class="rounded-xl bg-surface-2 p-3">
							<div class="flex items-baseline justify-between">
								<span class="caption">Riesgo del juez LLM</span>
								<span class="mono text-lg font-semibold">{judgeRiskText(score)}</span>
							</div>
							<div class="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-3">
								<div
									class="h-full rounded-full"
									style="width: {judgeRiskPct}%; background: {judgeRiskColor}"
								></div>
							</div>
						</div>
					</div>

					<dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
						<div>
							<dt class="caption">Prioridad</dt>
							<dd class="mono mt-1">{Math.round(score.priority)}</dd>
						</div>
						<div>
							<dt class="caption">Confianza</dt>
							<dd class="mono mt-1">{pct(score.confidence)}</dd>
						</div>
						<div>
							<dt class="caption">Riesgo de calidad</dt>
							<dd class="mono mt-1">{riskLabel(score.quality_risk)}</dd>
						</div>
						<div>
							<dt class="caption">Delta de calidad</dt>
							<dd class="mono mt-1">{score.quality_delta.toFixed(1)}</dd>
						</div>
						<div>
							<dt class="caption">Evidencias</dt>
							<dd class="mono mt-1">{score.evidence_count}</dd>
						</div>
						<div>
							<dt class="caption">Scan</dt>
							<dd class="mt-1">{scanAge(item?.scan_age_ns ?? 0)}</dd>
						</div>
					</dl>

					{#if score.judge_reason}
						<p class="mt-4 rounded-xl bg-surface-2 p-3 text-xs text-muted">{score.judge_reason}</p>
					{/if}

					<div class="mt-3 flex flex-wrap gap-2 text-[11px]">
						{#if score.conflict_detected}
							<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-partial">
								Conflicto entre fuentes
							</span>
						{/if}
						{#if score.volatile}
							<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-partial">
								Score volátil
							</span>
						{/if}
						<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-faint">
							calculado {formatTime(score.computed_at)}
						</span>
					</div>

					{#if score.evidence_count > 0 && evidence.length === 0}
						<p class="mt-3 text-[11px] text-faint">
							El score suma {score.evidence_count} señales, pero el informe todavía no expone su
							detalle.
						</p>
					{/if}

					{#if score.judge_model}
						<div class="mt-4 rounded-xl border border-line p-3">
							<h3 class="caption">Juez LLM · {score.judge_model}</h3>
							{#if score.judge_error}
								<p class="mt-2 text-xs text-error">{score.judge_error}</p>
							{:else}
								<p class="mt-2 text-sm font-medium">
									{score.judge_status ? STATUS_META[score.judge_status].label : 'Sin veredicto'}
									{#if score.judge_confidence != null}
										<span class="mono text-xs font-normal text-muted">
											· {pct(score.judge_confidence)}
										</span>
									{/if}
								</p>

							{/if}
							<p class="mt-2 text-[11px] text-faint">
								Heurística: {STATUS_META[score.heuristic_status].label}
							</p>
						</div>
					{/if}
				{:else}
					<p class="mt-3 text-sm text-muted">
						Sin score todavía: este establecimiento aún no se ha escaneado.
					</p>
				{/if}
			</section>
		</div>

		<!-- Website: the live URL plus the capture made on every sync -->
		<section class="glass mt-4 rounded-2xl p-5">
			<div class="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2">
				<h2 class="text-sm font-semibold">Website</h2>
				{#if screenshot?.capturedAt}
					<span class="text-[11px] text-faint">
						Capturada el {formatDate(screenshot.capturedAt)}
					</span>
				{/if}
			</div>

			<div class="mt-2 flex flex-wrap items-center gap-2 text-sm">
				{#if websiteURL}
					<a
						class="text-accent hover:underline"
						href={websiteURL}
						target="_blank"
						rel="noreferrer">{websiteURL} ↗</a
					>
					{#if item?.website_url && item.website_url !== est.website}
						<span class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] text-faint">
							descubierta por Google Maps
						</span>
					{/if}
				{:else}
					<p class="text-muted">Sin URL conocida.</p>
				{/if}
			</div>

			{#if screenshot}
				<div class="mt-4 overflow-hidden rounded-xl border border-line bg-surface-2/40">
					<img
						class="mx-auto w-full object-contain"
						src={screenshot.url}
						alt={`Captura de la web de ${est.name}`}
						loading="lazy"
					/>
				</div>
			{:else}
				<p class="mt-3 text-xs text-faint">
					Sin captura todavía: se guarda automáticamente en el próximo sync.
				</p>
			{/if}
		</section>

		<!-- Evidence: only when the report carries it -->
		{#if evidence.length > 0}
			<section class="glass mt-4 rounded-2xl p-5">
				<h2 class="text-sm font-semibold">Evidencia</h2>
				<p class="mt-1 text-xs text-faint">
					Señales que sostienen el score, de mayor a menor peso efectivo.
				</p>
				<div class="mt-3 overflow-x-auto">
					<table class="w-full text-sm">
						<thead>
							<tr class="border-b border-line text-left text-[11px] uppercase tracking-wider text-muted">
								<th class="py-2 pr-4 font-semibold">Señal</th>
								<th class="py-2 pr-4 font-semibold">Fuente</th>
								<th class="py-2 pr-4 font-semibold">Familia</th>
								<th class="py-2 pr-4 font-semibold">Recogida</th>
								<th class="py-2 pr-4 font-semibold">Valor</th>
								<th class="py-2 pr-4 font-semibold">Confianza</th>
								<th class="py-2 pr-4 font-semibold">Peso</th>
								<th class="py-2 font-semibold">Detalle</th>
							</tr>
						</thead>
						<tbody>
							{#each evidence as ev (ev.signal_id || `${ev.source}-${ev.kind}-${ev.collected_at}`)}
								<tr class="border-b border-line/60 last:border-0">
									<td class="py-2 pr-4 text-xs">{ev.label || ev.kind}</td>
									<td class="py-2 pr-4 text-xs text-muted">{ev.source}</td>
									<td class="py-2 pr-4 mono text-xs text-muted">{ev.family}</td>
									<td class="py-2 pr-4 text-xs text-muted">{formatTime(ev.collected_at)}</td>
									<td class="py-2 pr-4 mono text-xs">{ev.value.toFixed(2)}</td>
									<td class="py-2 pr-4 mono text-xs">{pct(ev.confidence)}</td>
									<td class="py-2 pr-4 mono text-xs">{ev.effective_weight.toFixed(2)}</td>
									<td class="py-2 text-xs text-muted">{ev.detail}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</section>
		{/if}
	{/if}
</div>
