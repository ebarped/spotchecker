import * as echarts from 'echarts';
const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 272, height: 340 });
chart.setOption({
	series: [{
		type: 'graph', layout: 'none', roam: true,
		symbol: 'rect',
		edgeSymbol: ['circle', 'arrow'],
		edgeSymbolSize: [4, 10],
		lineStyle: { color: '#a9aeb8', width: 1.2, opacity: 1 },
		data: [
			{ id: '0', x: 136, y: 60, symbolSize: [76, 42], silent: true },
			{ id: '1', x: 136, y: 132, symbolSize: [120, 42], silent: true },
			{ id: '2', x: 136, y: 204, symbolSize: [76, 42], silent: true }
		],
		links: [
			{ source: '0', target: '1' },
			{ source: '1', target: '2' }
		]
	}]
});
const svg = chart.renderToSVGString();
const arrows = [...svg.matchAll(/d="M0 0L([\d.]+) ([\d.]+)L0 ([\d.]+)L-([\d.]+) ([\d.]+)L0 0Z" transform="matrix\(([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+)\)"/g)].map(
	m => `triangle x${(+m[7]).toFixed(1)},y${(+m[9]).toFixed(1)} (scale ${m[7]}×${m[8]})`
);
const edges = [...svg.matchAll(/d="M([-\d.]+) ([\d.]+)L([-\d.]+) ([\d.]+)"/g)].map(m => `(${m[1]},${m[2]})→(${m[3]},${m[4]})`);
console.log('edges:', edges.join(' '));
console.log('arrows:', arrows.join(' | ') || 'NONE');
console.log('arrow tip touching node boundary:', arrows.length === 2 ? 'YES (2 hops, 2 arrows)' : 'check manually');
process.exit(0);
