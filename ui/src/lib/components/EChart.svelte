<script lang="ts">
	import * as echarts from 'echarts';

	/**
	 * Minimal Svelte wrapper around an Apache ECharts instance: init on mount,
	 * `setOption` whenever the (derived) option object changes, dispose on
	 * removal, and keep the canvas sized to the container.
	 */
	let {
		option,
		height = 260,
		merge = false
	}: {
		option: echarts.EChartsOption;
		height?: number | string;
		/** Merge mode keeps the roamed view (zoom/center) across updates. */
		merge?: boolean;
	} = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined = $state();

	$effect(() => {
		if (!el) return;
		chart = echarts.init(el, null, { renderer: 'canvas' });
		const observer = new ResizeObserver(() => chart?.resize());
		observer.observe(el);
		return () => {
			observer.disconnect();
			chart?.dispose();
			chart = undefined;
		};
	});

	$effect(() => {
		// merge keeps the roamed view (zoom/center) across updates; without
		// it every setOption would reset the user's pan and zoom.
		chart?.setOption(option, { notMerge: !merge });
	});
</script>

<div
	bind:this={el}
	style:height={typeof height === 'number' ? `${height}px` : height}
	class="w-full"
></div>
