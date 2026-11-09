<script lang="ts">
	import {
		coveredCount,
		STATUSES,
		STATUS_META,
		TIERS,
		TIER_COLORS,
		TIER_LABELS,
		statusCounts,
		tierCounts,
		type TurislabReport
	} from '../turislab';

	let { report }: { report: TurislabReport } = $props();

	const s = $derived(report.summary);
	const tiers = $derived(tierCounts(report.queue));
	const statuses = $derived(statusCounts(report.queue));
	const covered = $derived(coveredCount(report));
	const uncovered = $derived(Math.max(s.total - covered, 0));
	const urgent = $derived(s.p0 + s.p1);

	// Solid tile fills from the warm design system (white ink on all of them).
	const TILES = {
		establishments: '#3a6ea5',
		urgent: '#b85c38',
		closures: '#8a6428',
		coverage: '#22786f'
	} as const;

	const ICONS: Record<string, string> = {
		layers: 'M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5',
		activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
		zap: 'M13 2 3 14h9l-1 8 10-12h-9l1-8z',
		shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z'
	};

	interface Segment {
		key: string;
		count: number;
		pct: number;
		color: string;
		label: string;
	}

	function segments(entries: { key: string; count: number; color: string; label: string }[], total: number): Segment[] {
		return entries
			.filter((entry) => entry.count > 0)
			.map((entry) => ({
				...entry,
				pct: total ? (entry.count / total) * 100 : 0
			}));
	}

	interface Tile {
		key: string;
		title: string;
		color: string;
		icon: string;
		value: number | string;
		segments: Segment[];
		chips: { key: string; count: number; color: string; label: string }[];
		note: string;
	}

	const tierChips = $derived(
		TIERS.map((tier) => ({
			key: tier,
			count: tiers[tier],
			color: TIER_COLORS[tier],
			label: TIER_LABELS[tier].split(' · ')[0].toLowerCase()
		}))
	);

	const statusChips = $derived(
		STATUSES.map((status) => ({
			key: status,
			count: statuses[status],
			color: STATUS_META[status].color,
			label: STATUS_META[status].label.toLowerCase()
		}))
	);

	const tiles = $derived.by<Tile[]>(() => [
		{
			key: 'establishments',
			title: 'Establecimientos',
			color: TILES.establishments,
			icon: 'layers',
			value: s.total,
			segments: segments(
				TIERS.map((tier) => ({
					key: tier,
					count: tiers[tier],
					color: TIER_COLORS[tier],
					label: TIER_LABELS[tier]
				})),
				s.total
			),
			chips: tierChips,
			note: `${report.establishment_count} registrados · ${report.queue.length} en la cola`
		},
		{
			key: 'urgent',
			title: 'Visitas urgentes',
			color: TILES.urgent,
			icon: 'zap',
			value: urgent,
			segments: segments(
				(['P0', 'P1'] as const).map((tier) => ({
					key: tier,
					count: tiers[tier],
					color: TIER_COLORS[tier],
					label: TIER_LABELS[tier]
				})),
				s.total
			),
			chips: tierChips.filter((chip) => chip.key === 'P0' || chip.key === 'P1'),
			note: `P0 y P1 de ${s.total} establecimientos puntuados`
		},
		{
			key: 'closures',
			title: 'Cierres confirmados',
			color: TILES.closures,
			icon: 'shield',
			value: s.closure_confirmed,
			segments: segments(statusChips, report.queue.length),
			chips: statusChips,
			note: `${s.low_confidence} con baja confianza`
		},
		{
			key: 'coverage',
			title: 'Cobertura',
			color: TILES.coverage,
			icon: 'activity',
			value: `${Math.round(s.coverage_pct)}%`,
			segments: segments(
				[
					{ key: 'covered', count: covered, color: 'var(--color-ok)', label: 'con datos' },
					{ key: 'uncovered', count: uncovered, color: 'var(--color-skipped)', label: 'sin datos' }
				],
				s.total
			),
			chips: [
				{ key: 'covered', count: covered, color: 'var(--color-ok)', label: 'con datos' },
				{ key: 'uncovered', count: uncovered, color: 'var(--color-skipped)', label: 'sin datos' },
				{ key: 'low', count: s.low_confidence, color: 'var(--color-partial)', label: 'baja confianza' }
			],
			note: `${covered} de ${s.total} con evidencia recogida`
		}
	]);
</script>

{#snippet icon(name: string)}
	<svg
		class="size-[18px] opacity-80"
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width="1.8"
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"
	>
		<path d={ICONS[name]} />
	</svg>
{/snippet}

<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
	{#each tiles as tile (tile.key)}
		<div class="tile rounded-2xl p-5 text-white" style="--tile: {tile.color}">
			<div class="flex items-center justify-between">
				<p class="text-[11px] font-semibold tracking-wider uppercase opacity-80">{tile.title}</p>
				{@render icon(tile.icon)}
			</div>
			<p class="mt-2 text-4xl font-bold tracking-tight">{tile.value}</p>
			<div class="mt-4 flex h-2 gap-0.5 rounded-full bg-white/15">
				{#each tile.segments as seg (seg.key)}
					<div
						class="h-full rounded-full"
						style="width: {seg.pct}%; background: {seg.color}"
						title="{seg.label}: {seg.count} ({seg.pct.toFixed(1)}%)"
					></div>
				{/each}
			</div>
			<div class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs">
				{#each tile.chips as chip (chip.key)}
					<span class="inline-flex items-center gap-1.5 opacity-95">
						<span
							class="size-2 rounded-full ring-1 ring-white/40"
							style="background-color: {chip.color}"
						></span>
						<span class="font-semibold">{chip.count}</span>
						<span class="opacity-80">{chip.label}</span>
					</span>
				{/each}
			</div>
			<p class="mt-2 text-xs opacity-65">{tile.note}</p>
		</div>
	{/each}
</div>
