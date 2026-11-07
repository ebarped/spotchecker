import * as echarts from 'echarts';
const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 100, height: 100 });
chart.setOption({ series: [{ type: 'graph', layout: 'none', data: [
	{ id: 'up', x: 30, y: 50, symbol: 'arrow', symbolSize: 20, itemStyle: { color: '#4ea8dc' } },
	{ id: 'down', x: 70, y: 50, symbol: 'arrow', symbolSize: 20, symbolRotate: 180, itemStyle: { color: '#e8875e' } }
] }] });
const svg = chart.renderToSVGString();
const shapes = [...svg.matchAll(/<path[^>]*d="([^"]+)"[^>]*transform="matrix\(([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+)\)"/g)];
shapes.forEach((m, i) => {
	// min/max y of the raw path (before transform): where is the tip?
	const ys = [...m[1].matchAll(/([-\d.]+)(?=[ ]|$)/g)].map(x => +x[1]).filter((_, idx) => idx % 2 === 1);
	console.log(`shape ${i} (min-y ${Math.min(...ys).toFixed(2)}, max-y ${Math.max(...ys).toFixed(2)}) rotate-transform: matrix(${m[2]},${m[3]},${m[4]},${m[5]})`);
});
process.exit(0);
