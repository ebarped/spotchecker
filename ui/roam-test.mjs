import * as echarts from 'echarts';

function render(name, series) {
	const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 272, height: 304 });
	chart.setOption({ series });
	const svg = chart.renderToSVGString();
	const matrices = [...svg.matchAll(/transform="matrix\(([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+),([-\d.e]+)\)"/g)].map(
		m => `(${(+m[5]).toFixed(1)},${(+m[6]).toFixed(1)})x${(+m[1]).toFixed(2)}`
	);
	console.log(name.padEnd(34), '|', matrices.slice(0, 4).join(' '));
	chart.dispose();
}

const data = [
	{ id: '0', x: 136, y: 44, symbol: 'rect', symbolSize: [76, 42], silent: true },
	{ id: '1', x: 136, y: 140, symbol: 'rect', symbolSize: [120, 42], silent: true },
	{ id: '2', x: 136, y: 236, symbol: 'rect', symbolSize: [76, 42], silent: true }
];
const links = [{ source: '0', target: '1' }, { source: '1', target: '2' }];

render('A: no roam', { type: 'graph', layout: 'none', symbol: 'rect', data, links });
render('B: roam, no zoom/center', { type: 'graph', layout: 'none', roam: true, symbol: 'rect', data, links });
render('C: roam + zoom1 + center', { type: 'graph', layout: 'none', roam: true, zoom: 1, center: [136, 140], symbol: 'rect', data, links });
render('D: no roam + zoom1 + center', { type: 'graph', layout: 'none', zoom: 1, center: [136, 140], symbol: 'rect', data, links });
process.exit(0);
