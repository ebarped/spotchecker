<script lang="ts">
	import PhaseFlow from '../../../lib/components/PhaseFlow.svelte';
	import PipelineRail from '../../../lib/components/PipelineRail.svelte';
	import StatusChip from '../../../lib/components/StatusChip.svelte';
	import { PIPELINE_DETAILS, phaseDetail } from '../../../lib/pipeline';

	let { data }: { data: { slug: string } } = $props();

	const detail = $derived(phaseDetail(data.slug));
	const index = $derived(detail ? PIPELINE_DETAILS.findIndex((p) => p.id === detail.id) : -1);
	const prev = $derived(index > 0 ? PIPELINE_DETAILS[index - 1] : null);
	const next = $derived(
		index >= 0 && index < PIPELINE_DETAILS.length - 1 ? PIPELINE_DETAILS[index + 1] : null
	);

	function dot(status: 'done' | 'partial' | 'todo' | undefined): string {
		return status === 'done'
			? 'bg-ok'
			: status === 'partial'
				? 'bg-partial'
				: status === 'todo'
					? 'bg-skipped'
					: 'bg-faint';
	}
</script>

<svelte:head>
	<title>{detail ? detail.name : 'Fase'} · SpotChecker</title>
</svelte:head>

<div class="mx-auto w-full max-w-5xl px-4 py-10 sm:px-6">
	<nav class="flex flex-wrap items-center gap-2 text-xs">
		<a href="/how-it-works" class="glass glass-hover rounded-full px-3 py-1 text-muted">
			← Cómo funciona
		</a>
		<a href="/turislab" class="glass glass-hover rounded-full px-3 py-1 text-muted">
			Cola de inspección
		</a>
	</nav>

	{#if !detail}
		<section class="glass mx-auto mt-10 max-w-2xl rounded-2xl p-8 text-center">
			<h1 class="text-2xl font-semibold">Esa fase no existe</h1>
			<p class="mt-2 text-sm text-muted">El pipeline tiene seis fases; esta no es una de ellas.</p>
			<a
				href="/how-it-works"
				class="mt-4 inline-block rounded-xl border border-line bg-surface-2 px-4 py-2 text-sm transition-colors hover:border-line-strong"
			>
				Volver a Cómo funciona
			</a>
		</section>
	{:else}
		<header class="mt-5">
			<div class="flex flex-wrap items-center gap-2">
				<span class="glass rounded-full px-3 py-1 font-mono text-xs font-semibold text-accent">
					Fase {detail.id}
				</span>
				<StatusChip status={detail.status} />
				<span
					class="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11px] font-medium text-muted"
				>
					{detail.artifact}
				</span>
			</div>
			<h1 class="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">{detail.name}</h1>
			<p class="mt-2 max-w-3xl text-sm text-muted">{detail.tagline}</p>
		</header>

		<div class="mt-6">
			<PipelineRail current={detail.slug} />
		</div>

		<section class="glass mt-6 rounded-2xl p-5">
			<h2 class="text-sm font-semibold">En una frase</h2>
			<p class="mt-2 text-sm leading-relaxed text-muted">{detail.summary}</p>
			<dl class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
				{#each detail.facts as fact (fact.label)}
					<div class="rounded-xl bg-surface-2 p-3">
						<dt class="text-[11px] font-semibold tracking-wider uppercase text-faint">
							{fact.label}
						</dt>
						<dd class="mt-1 text-sm font-medium">{fact.value}</dd>
					</div>
				{/each}
			</dl>
		</section>

		<section class="mt-8">
			<h2 class="text-lg font-semibold">El flujo</h2>
			<p class="mt-1 text-sm text-muted">
				De izquierda a derecha y de arriba abajo: así se transforma el artefacto de esta fase.
			</p>
			<div class="mt-4">
				<PhaseFlow steps={detail.flow} />
			</div>
		</section>

		{#if detail.bars}
			<section class="glass mt-8 rounded-2xl p-5">
				<h2 class="text-sm font-semibold">{detail.bars.title}</h2>
				<div class="mt-4 space-y-3">
					{#each detail.bars.items as item (item.label)}
						<div>
							<div class="flex items-baseline justify-between gap-3 text-xs">
								<span class="font-medium text-ink">{item.label}</span>
								<span class="mono shrink-0 text-muted">{item.note}</span>
							</div>
							<div class="mt-1.5 h-2.5 overflow-hidden rounded-full bg-surface-2">
								<div
									class="h-full rounded-full {dot(item.status)}"
									style="width: {item.pct}%"
								></div>
							</div>
						</div>
					{/each}
				</div>
			</section>
		{/if}

		{#if detail.table}
			<section class="glass mt-8 overflow-hidden rounded-2xl">
				<div class="border-b border-line px-5 py-4">
					<h2 class="text-sm font-semibold">{detail.table.title}</h2>
				</div>
				<div class="overflow-x-auto">
					<table class="w-full text-sm">
						<thead>
							<tr class="border-b border-line text-left text-[11px] uppercase tracking-wider text-muted">
								{#each detail.table.columns as column (column)}
									<th class="px-5 py-3 font-semibold">{column}</th>
								{/each}
							</tr>
						</thead>
						<tbody>
							{#each detail.table.rows as row (row.cells.join('|'))}
								<tr class="border-b border-line/60 last:border-0">
									<td class="px-5 py-3">
										<span class="inline-flex items-center gap-2">
											<span class="size-1.5 shrink-0 rounded-full {dot(row.status)}"></span>
											<span class="mono text-xs">{row.cells[0]}</span>
										</span>
									</td>
									{#each row.cells.slice(1) as cell, i (i)}
										<td
											class="px-5 py-3 {i === row.cells.length - 2
												? 'font-medium text-ink'
												: 'text-muted'}"
										>
											{cell}
										</td>
									{/each}
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</section>
		{/if}

		<div class="mt-8 grid items-start gap-4 lg:grid-cols-2">
			<section class="glass rounded-2xl border-ok/20 p-5">
				<h2 class="flex items-center gap-2 text-sm font-semibold text-ok">
					<svg
						class="size-4"
						viewBox="0 0 24 24"
						fill="none"
						stroke="currentColor"
						stroke-width="2"
						aria-hidden="true"
					>
						<path stroke-linecap="round" stroke-linejoin="round" d="m5 13 4 4L19 7" />
					</svg>
					Implementado hoy
				</h2>
				<ul class="mt-3 space-y-2 text-sm text-muted">
					{#each detail.implemented as item (item)}
						<li class="flex gap-2">
							<span class="mt-2 size-1.5 shrink-0 rounded-full bg-ok"></span>
							<span>{item}</span>
						</li>
					{/each}
				</ul>
			</section>

			<section class="glass rounded-2xl border-partial/20 p-5">
				<h2 class="flex items-center gap-2 text-sm font-semibold text-partial">
					<svg
						class="size-4"
						viewBox="0 0 24 24"
						fill="none"
						stroke="currentColor"
						stroke-width="2"
						aria-hidden="true"
					>
						<circle cx="12" cy="12" r="9" />
						<path stroke-linecap="round" d="M12 7v6m0 3.5v.5" />
					</svg>
					Pendiente / TODO
				</h2>
				<ul class="mt-3 space-y-2 text-sm text-muted">
					{#each detail.todos as item (item)}
						<li class="flex gap-2">
							<span
								class="mt-0.5 shrink-0 rounded bg-partial/10 px-1.5 py-0.5 text-[10px] font-bold text-partial"
							>
								TODO
							</span>
							<span>{item}</span>
						</li>
					{/each}
				</ul>
			</section>
		</div>

		{#if detail.note}
			<section class="glass mt-4 rounded-2xl p-5 text-sm leading-relaxed text-muted">
				<span class="font-semibold text-ink">Nota: </span>{detail.note}
			</section>
		{/if}

		<nav class="mt-8 grid gap-3 sm:grid-cols-2" aria-label="Fases anterior y siguiente">
			{#if prev}
				<a
					href="/how-it-works/{prev.slug}"
					class="glass glass-hover rounded-2xl p-4 text-left"
				>
					<span class="text-xs text-faint">← Fase {prev.id}</span>
					<p class="mt-1 text-sm font-semibold">{prev.name}</p>
				</a>
			{/if}
			{#if next}
				<a
					href="/how-it-works/{next.slug}"
					class="glass glass-hover rounded-2xl p-4 text-right sm:col-start-2"
				>
					<span class="text-xs text-faint">Fase {next.id} →</span>
					<p class="mt-1 text-sm font-semibold">{next.name}</p>
				</a>
			{/if}
		</nav>
	{/if}
</div>
