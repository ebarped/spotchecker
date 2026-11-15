<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, categoryAxis, riskRamp, tooltip, valueAxis } from '../echarts.svelte';
	import { riskBuckets, type TurislabReport } from '../turislab';
	import EChart from './EChart.svelte';

	let { report }: { report: TurislabReport } = $props();

	const pal = $derived.by(() => {
		return chartPalette();
	});

	const buckets = $derived(riskBuckets(report.queue));

	// One colour per 20-point bucket: green at 0–20% through amber up to red
	// at 80–100%, so the bar height and the colour tell the same story.
	const ramp = $derived(riskRamp(pal));

	const option = $derived.by<EChartsOption>(() => {
		const axis = categoryAxis(
			buckets.map((bucket) => bucket.label),
			pal
		);
		return {
			tooltip: {
				...tooltip(pal),
				trigger: 'axis',
				axisPointer: { type: 'shadow' },
				valueFormatter: (value) => `${value} establecimientos`
			},
			grid: { left: 34, right: 24, top: 18, bottom: 28 },
			xAxis: {
				...axis,
				// Tint the tick labels too: the urgency is legible even when a
				// bucket is empty and its bar has nothing to show.
				axisLabel: {
					...axis.axisLabel,
					color: (_value?: string | number, index?: number) => ramp[index ?? 0] ?? pal.muted
				}
			},
			yAxis: { ...valueAxis(pal), minInterval: 1 },
			series: [
				{
					type: 'bar',
					barWidth: '52%',
					itemStyle: { borderRadius: [4, 4, 0, 0] },
					data: buckets.map((bucket, i) => ({
						value: bucket.count,
						itemStyle: { color: ramp[i] }
					}))
				}
			]
		};
	});
</script>

<EChart {option} height={250} />
