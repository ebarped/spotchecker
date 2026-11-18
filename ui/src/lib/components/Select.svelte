<script lang="ts">
	export interface Option {
		value: string;
		label: string;
		/** Optional color dot (e.g. tier color) shown before the label */
		color?: string;
		/** Optional SVG path d for a 24x24 stroke icon shown before the label */
		icon?: string;
	}

	/** Custom styled select (combobox) with optional leading icon, similar to team-report style. */
	let {
		label,
		icon,
		hideLabel = false,
		value = $bindable(''),
		options,
		onchange
	}: {
		label: string;
		/** SVG path d for a 24x24 stroke icon shown at start */
		icon?: string;
		hideLabel?: boolean;
		value: string;
		options: Option[];
		onchange?: (v: string) => void;
	} = $props();

	let open = $state(false);
	let active = $state(0);
	let root: HTMLDivElement | undefined = $state();
	const id = `sel-${Math.random().toString(36).slice(2, 8)}`;

	const current = $derived(options.find((o) => o.value === value) ?? options[0]);

	function openMenu() {
		active = Math.max(0, options.findIndex((o) => o.value === value));
		open = true;
	}

	function choose(o: Option) {
		value = o.value;
		open = false;
		onchange?.(o.value);
	}

	function onKey(e: KeyboardEvent) {
		if (!open) {
			if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) {
				e.preventDefault();
				openMenu();
			}
			return;
		}
		if (e.key === 'Escape') open = false;
		else if (e.key === 'ArrowDown') active = (active + 1) % options.length;
		else if (e.key === 'ArrowUp') active = (active - 1 + options.length) % options.length;
		else if (e.key === 'Enter' || e.key === ' ') choose(options[active]);
		else return;
		e.preventDefault();
	}
</script>

<svelte:window onclick={(e) => open && root && !root.contains(e.target as Node) && (open = false)} />

<div bind:this={root} class="relative inline-block">
	<button
		type="button"
		role="combobox"
		aria-haspopup="listbox"
		aria-expanded={open}
		aria-controls={id}
		aria-label={label}
		onclick={() => (open ? (open = false) : openMenu())}
		onkeydown={onKey}
		class="glass glass-hover inline-flex h-9 items-center gap-2 rounded-full px-3.5 text-sm"
	>
		{#if icon}
			<svg class="size-4 shrink-0 text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={icon} /></svg>
		{/if}
		{#if !hideLabel}
			<span class="text-[10px] uppercase tracking-wider text-faint">{label}</span>
		{/if}
		{#if current?.icon}
			<svg class="size-4 shrink-0 text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={current.icon} /></svg>
		{/if}
		{#if current?.color}
			<span class="size-2.5 shrink-0 rounded-full" style="background: {current.color}"></span>
		{/if}
		<span class="font-medium">{current?.label}</span>
		<svg class="size-3.5 text-muted transition-transform {open ? 'rotate-180' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
			<path d="m6 9 6 6 6-6" />
		</svg>
	</button>

	{#if open}
		<ul
			{id}
			role="listbox"
			class="glass absolute top-full left-0 z-30 mt-2 min-w-full rounded-2xl p-1.5 shadow-lg"
		>
			{#each options as o, i (o.value)}
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<li
					role="option"
					aria-selected={o.value === value}
					onclick={() => choose(o)}
					onmouseenter={() => (active = i)}
					class="flex cursor-pointer items-center gap-2.5 rounded-xl px-3 py-2 text-sm whitespace-nowrap {i === active ? 'bg-surface-2' : ''} {o.value === value ? 'font-semibold' : ''}"
				>
					{#if o.icon}
						<svg class="size-4 shrink-0 text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={o.icon} /></svg>
					{/if}
					{#if o.color}
						<span class="size-2.5 shrink-0 rounded-full" style="background: {o.color}"></span>
					{/if}
					<span class="flex-1">{o.label}</span>
					{#if o.value === value}
						<svg class="size-3.5 text-accent" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>