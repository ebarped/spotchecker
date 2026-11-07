import * as echarts from 'echarts';
const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 100, height: 100 });
chart.setOption({ series: [{ type: 'graph', layout: 'none', data: [
	{ id: 'x', x: 50, y: 50, symbol: 'arrow', symbolSize: 20 }
] }] });
const svg = chart.renderToSVGString();
const d = [...svg.matchAll(/<path[^>]*d="([^"]+)"/g)].map(m => m[1]).find(d => d.includes('M0') || d.startsWith('M-'));
console.log('arrow d:', d);
process.exit(0);
