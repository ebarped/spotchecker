<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, legend, tooltip } from '../echarts.svelte';
	import { TIERS, tierCounts, type Tier, type TurislabReport } from '../turislab';
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
		collect_data: pal.skipped,
		por_procesar: pal.faint
	}));

	const counts = $derived(tierCounts(report.queue));

	const SHORT: Record<Tier, string> = {
		P0: 'P0',
		P1: 'P1',
		P2: 'P2',
		P3: 'P3',
		collect_data: 'Recoger datos',
		por_procesar: 'Por procesar'
	};

	const data = $derived(
		TIERS.map((tier) => ({
			name: SHORT[tier],
			value: counts[tier],
			itemStyle: { color: tierColor[tier] }
		}))
	);
	const covered = $derived(Math.round(report.summary.coverage_pct));

	const option = $derived.by<EChartsOption>(() => ({
		tooltip: { ...tooltip(pal), valueFormatter: (value) => `${value} establecimientos` },
		legend: legend(pal),
		series: [
			{
				type: 'pie',
				radius: ['64%', '84%'],
				center: ['50%', '42%'],
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
	<EChart {option} height={250} />
	<div class="pointer-events-none absolute inset-x-0 top-[30%] flex flex-col items-center">
		<span class="text-3xl font-semibold tracking-tight">{covered}%</span>
		<span class="text-xs text-muted">con datos</span>
	</div>
</div>
