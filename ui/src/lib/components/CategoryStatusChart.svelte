<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, categoryAxis, legend, tooltip, valueAxis } from '../echarts.svelte';
	import { theme } from '../theme.svelte';
	import {
		STATUSES,
		STATUS_META,
		statusRowsByCategory,
		type SuggestedStatus,
		type TurislabReport
	} from '../turislab';
	import EChart from './EChart.svelte';

	let { report }: { report: TurislabReport } = $props();

	const pal = $derived.by(() => {
		void theme.theme;
		return chartPalette();
	});

	const statusColor = $derived.by<Record<SuggestedStatus, string>>(() => ({
		closure_confirmed: pal.ko,
		closure_suspected: pal.partial,
		closed_temporarily: pal.empty,
		seasonal: pal.accent3,
		active: pal.ok,
		unknown: pal.skipped
	}));

	const rows = $derived(statusRowsByCategory(report.queue));
	const height = $derived(Math.max(200, rows.length * 30 + 70));

	function radius(index: number, row: (typeof rows)[number]): [number, number, number, number] {
		const present = STATUSES.filter((status) => row.counts[status] > 0);
		const first = present[0];
		const last = present[present.length - 1];
		const status = STATUSES[index];
		if (first === status && last === status) return [4, 4, 4, 4];
		if (first === status) return [4, 0, 0, 4];
		if (last === status) return [0, 4, 4, 0];
		return [0, 0, 0, 0];
	}

	const option = $derived.by<EChartsOption>(() => ({
		tooltip: {
			...tooltip(pal),
			trigger: 'axis',
			axisPointer: { type: 'shadow' },
			formatter: (params: unknown) => {
				const list = params as {
					marker: string;
					seriesName: string;
					value: number;
					data: { groupLabel: string };
				}[];
				const body = list
					.filter((p) => p.value > 0)
					.map((p) => `<div>${p.marker} ${p.seriesName}&nbsp;&nbsp;<b>${p.value}</b></div>`)
					.join('');
				return `<div style="font-weight:600;margin-bottom:4px">${list[0].data.groupLabel}</div>${body}`;
			}
		},
		legend: legend(pal, 'top-right'),
		grid: { left: 110, right: 28, top: 34, bottom: 12 },
		xAxis: { ...valueAxis(pal), minInterval: 1 },
		yAxis: { ...categoryAxis(rows.map((r) => r.label), pal, 100), inverse: true },
		series: STATUSES.map((status, index) => ({
			name: STATUS_META[status].label,
			type: 'bar' as const,
			stack: 'categories',
			barWidth: 13,
			itemStyle: { color: statusColor[status] },
			data: rows.map((row) => ({
				value: row.counts[status],
				groupLabel: row.label,
				itemStyle: { borderRadius: radius(index, row) }
			}))
		}))
	}));
</script>

{#if rows.length > 0}
	<EChart {option} {height} />
{:else}
	<p class="py-8 text-center text-sm text-faint">Todavía no hay establecimientos puntuados.</p>
{/if}
