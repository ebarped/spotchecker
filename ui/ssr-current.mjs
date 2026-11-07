import * as echarts from 'echarts';
import { writeFileSync } from 'node:fs';
const W = 272, H = 304, GAP = 96;
const flow = ['question', 'tool: list_findings', 'tool: get_dashboard_by_uid', 'answer', 'question'];
const totalHeight = flow.length * GAP + 60;
const zoom = Math.min(1.15, Math.max(0.3, (H - 30) / totalHeight));
const data = flow.map((label, index) => ({
	id: String(index), x: W / 2, y: 44 + index * GAP,
	symbol: 'rect', symbolSize: [76, 42],
	label: { show: true, position: 'inside', formatter: () => (index === 0 || index === 3 ? 'question' : `tool:\n${label.replace('tool: ', '')}`), silent: true
	}
}));
const links = flow.slice(0, -1).map((_, i) => ({ source: String(i), target: String(i + 1) }));
const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: W, height: H });
chart.setOption({ series: [{
	type: 'graph', layout: 'none', roam: true, zoom, center: [W / 2, totalHeight / 2],
	symbol: 'rect',
	edgeSymbol: ['none', 'arrow'], edgeSymbolSize: 12,
	lineStyle: { color: '#a9aeb8', width: 1.4, opacity: 1, curveness: 0 },
	data, links
}] });
const svg = chart.renderToSVGString();
writeFileSync('/tmp/opencode/ssr-current.svg', svg);
// every path: d head + transform
[...svg.matchAll(/<path[^>]*d="([^"]{0,46})"[^>]*transform="matrix\(([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+)\)"/g)].forEach((m, i) => {
	const [, d, a, b, c, dd, e] = m;
	console.log(`path ${i}: (${(+dd).toFixed(1)},${(+e).toFixed(1)}) scale(${a},${b}) d=${d}`);
});
process.exit(0);
