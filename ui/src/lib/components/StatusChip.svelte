<script lang="ts">
	import { PHASE_STATUS, type PhaseStatus } from '../pipeline';

	let { status, label }: { status: PhaseStatus; label?: string } = $props();

	// Solid traffic-light pills: green = implemented, amber = partial,
	// red = pending. The dark ink stays readable on all three backgrounds
	// in both themes.
	const tone = $derived(
		status === 'done'
			? 'bg-ok text-[#1c2b1f]'
			: status === 'partial'
				? 'bg-partial text-[#33270e]'
				: 'bg-ko text-[#36140d]'
	);
</script>

<span
	class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[11px] font-bold {tone}"
>
	<span class="size-1.5 rounded-full bg-current"></span>
	{label ?? PHASE_STATUS[status].label}
</span>
