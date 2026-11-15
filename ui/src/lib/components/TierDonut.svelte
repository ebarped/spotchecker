<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, legend, tooltip } from '../echarts.svelte';
	import {
		TIERS,
		TIER_CHART_LABELS,
		tierCounts,
		type Tier,
		type TurislabReport
	} from '../turislab';
	import EChart from './EChart.svelte';

	let { report }: { report: TurislabReport } = $props();

	const pal = $derived.by(() => {
		return chartPalette();
	});

	const tierColor = $derived.by<Record<Tier, string>>(() => ({
		P0: pal.ko,
		P1: pal.partial,
		P2: pal.empty,
		P3: pal.ok,
		collect_data: pal.accent2,
		por_procesar: pal.faint
	}));

	const counts = $derived(tierCounts(report.queue));

	const data = $derived(
		TIERS.map((tier) => ({
			name: TIER_CHART_LABELS[tier],
			value: counts[tier],
			itemStyle: { color: tierColor[tier] }
		}))
	);
	const covered = $derived(Math.round(report.summary.coverage_pct));

	// The donut sits above the legend instead of centred: with six
	// descriptive labels the legend wraps to two or three rows, and a
	// centred donut would grow into it on narrow cards.
	const CENTER_Y = '42%';

	const option = $derived.by<EChartsOption>(() => ({
		tooltip: { ...tooltip(pal), valueFormatter: (value) => `${value} establecimientos` },
		// Smaller gap than the default: the labels are longer now.
		legend: { ...legend(pal), itemGap: 10 },
		series: [
			{
				type: 'pie',
				radius: ['46%', '64%'],
				center: ['50%', CENTER_Y],
				padAngle: 2,
				itemStyle: { borderRadius: 4, borderColor: pal.overlay, borderWidth: 2 },
				label: { show: false },
				emphasis: { scaleSize: 4 },
				data
			}
		]
	}));
</script>

<div class="relative">
	<EChart {option} height={280} />
	<div
		class="pointer-events-none absolute inset-x-0 flex -translate-y-1/2 flex-col items-center"
		style="top: {CENTER_Y}"
	>
		<span class="text-3xl font-semibold tracking-tight">{covered}%</span>
		<span class="text-xs text-muted">con datos</span>
	</div>
</div>
