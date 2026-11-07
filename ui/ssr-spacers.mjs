import * as echarts from 'echarts';
const W = 272, H = 304, GAP = 96;
const labels = ['question', 'tool: list_findings', 'tool: get_dashboard_by_uid', 'answer', 'question'];
const totalHeight = labels.length * GAP + 60;
const usableH = H - 84;
const step = Math.min(120, usableH / (labels.length - 1));
const chainH = step * (labels.length - 1);
const chainTop = 44 + (usableH - chainH) / 2;
const data = [
	...labels.map((label, index) => ({
		id: String(index), x: W / 2, y: chainTop + index * step,
		symbol: 'rect', symbolSize: index === 0 || index === 3 ? [76, 42] : [120, 42],
		silent: true
	})),
	{ id: 'spacer-left', x: 16, y: H / 2, symbolSize: 0.1, itemStyle: { opacity: 0 } },
	{ id: 'spacer-right', x: W - 16, y: H / 2, symbolSize: 0.1, itemStyle: { opacity: 0 } }
];
const links = labels.slice(0, -1).map((_, i) => ({ source: String(i), target: String(i + 1) }));
const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: W, height: H });
chart.setOption({
	series: [{
		type: 'graph', layout: 'none', roam: true, zoom: 1, scaleLimit: { min: 0.4, max: 4 },
		symbol: 'rect', edgeSymbol: ['none', 'arrow'], edgeSymbolSize: 12,
		lineStyle: { color: '#a9aeb8', width: 1.4, opacity: 1 }, data, links
	}]
});
const svg = chart.renderToSVGString();
[...svg.matchAll(/<path[^>]*d="([^"]{0,40})"[^>]*transform="matrix\(([-\d.e]+),0,0,([-\d.e]+),([-\d.e]+),([-\d.e]+)\)"/g)].forEach((m, i) => {
	console.log(`path ${i}: at=(${(+m[5]).toFixed(1)},${(+m[6]).toFixed(1)}) scale=(${m[2]},${m[3]}) d=${m[1].slice(0, 24)}`);
});
process.exit(0);
