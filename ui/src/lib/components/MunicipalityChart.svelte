<script lang="ts">
	import type { EChartsOption } from 'echarts';
	import { chartPalette, categoryAxis, legend, tooltip, valueAxis } from '../echarts.svelte';
	import { TIERS, tierRowsByMunicipality, type Tier, type TurislabReport } from '../turislab';
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
		collect_data: pal.skipped
	}));

	const SHORT: Record<Tier, string> = {
		P0: 'P0 · Visitar ya',
		P1: 'P1 · Visitar pronto',
		P2: 'P2 · Programar',
		P3: 'P3 · Vigilar',
		collect_data: 'Recoger datos'
	};

	// Six municipalities fit the fixed 250px chart with two-line labels;
	// the rest are folded into the "Otros" row.
	const rows = $derived(tierRowsByMunicipality(report.queue, 6));

	// Stacked horizontal bars read left-to-right: the first non-empty
	// segment carries the left radius, the last one the right radius.
	function radius(index: number, row: (typeof rows)[number]): [number, number, number, number] {
		const present = TIERS.filter((tier) => row.counts[tier] > 0);
		const first = present[0];
		const last = present[present.length - 1];
		const tier = TIERS[index];
		if (first === tier && last === tier) return [4, 4, 4, 4];
		if (first === tier) return [4, 0, 0, 4];
		if (last === tier) return [0, 4, 4, 0];
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
		grid: { left: 170, right: 28, top: 34, bottom: 12 },
		xAxis: { ...valueAxis(pal), minInterval: 1 },
		yAxis: { ...categoryAxis(rows.map((r) => r.label), pal, 160), inverse: true },
		series: TIERS.map((tier, index) => ({
			name: SHORT[tier],
			type: 'bar' as const,
			stack: 'municipalities',
			barWidth: 13,
			itemStyle: { color: tierColor[tier] },
			data: rows.map((row) => ({
				value: row.counts[tier],
				groupLabel: row.label,
				itemStyle: { borderRadius: radius(index, row) }
			}))
		}))
	}));
</script>

{#if rows.length > 0}
	<EChart {option} height={250} />
{:else}
	<p class="py-8 text-center text-sm text-faint">
		Todavía no hay establecimientos puntuados en ningún municipio.
	</p>
{/if}
