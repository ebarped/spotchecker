<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, categoryAxis, tooltip, valueAxis } from '../echarts.svelte';
	import { riskBuckets, type TurislabReport } from '../turislab';
	import EChart from './EChart.svelte';

	let { report }: { report: TurislabReport } = $props();

	const pal = $derived.by(() => {
		return chartPalette();
	});

	const buckets = $derived(riskBuckets(report.queue));

	const option = $derived.by<EChartsOption>(() => ({
		tooltip: {
			...tooltip(pal),
			trigger: 'axis',
			axisPointer: { type: 'shadow' },
			valueFormatter: (value) => `${value} establecimientos`
		},
		grid: { left: 34, right: 24, top: 18, bottom: 28 },
		xAxis: categoryAxis(
			buckets.map((bucket) => bucket.label),
			pal
		),
		yAxis: { ...valueAxis(pal), minInterval: 1 },
		series: [
			{
				type: 'bar',
				barWidth: '52%',
				// Neutral fill: the histogram describes the distribution, and a
				// wall of red reads as danger even in the low buckets.
				itemStyle: { color: pal.muted, borderRadius: [4, 4, 0, 0] },
				data: buckets.map((bucket) => bucket.count)
			}
		]
	}));
</script>

<EChart {option} height={250} />
