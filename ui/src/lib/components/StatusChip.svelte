<script lang="ts">
	import { PHASE_STATUS, type PhaseStatus } from '../pipeline';

	let { status, label }: { status: PhaseStatus; label?: string } = $props();

	const tone = $derived(
		status === 'done'
			? 'bg-ok/10 text-ok'
			: status === 'partial'
				? 'bg-partial/10 text-partial'
				: 'bg-skipped/10 text-skipped'
	);
</script>

<span
	class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold {tone}"
>
	<span class="size-1.5 rounded-full bg-current"></span>
	{label ?? PHASE_STATUS[status].label}
</span>
