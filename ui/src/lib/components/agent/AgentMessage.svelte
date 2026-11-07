<script lang="ts">
	import logo from '#lib/assets/logo.png';
	import { agent, type ChatMessage } from '../../agent.svelte';
	import { renderMarkdown } from '../../markdown';

	let { message, live = false }: { message: ChatMessage; live?: boolean } = $props();

	const html = $derived(message.role === 'assistant' ? renderMarkdown(message.text) : '');
</script>

{#if message.role === 'user'}
	<div class="flex justify-end">
		<p class="max-w-[85%] rounded-2xl rounded-br-md bg-accent/15 px-4 py-2.5 text-sm text-ink">
			{message.text}
		</p>
	</div>
{:else}
	<div class="flex max-w-[94%] items-start gap-2.5">
		<img
			src={logo}
			alt=""
			class="size-7 shrink-0 rounded-full border border-line bg-surface-2"
		/>
		<div class="min-w-0 flex-1 space-y-2">
			{#if message.steps.length > 0}
				<div class="flex flex-wrap gap-1.5">
					{#each message.steps as step, i (i)}
						<details class="max-w-full">
							<summary
								class="mono inline-flex cursor-pointer list-none items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium transition-colors [&::-webkit-details-marker]:hidden
								{step.name === 'error'
									? 'border-error/40 bg-error/10 text-error hover:bg-error/20'
									: 'border-accent/40 bg-accent/10 text-accent hover:bg-accent/20'}"
							>
								{#if step.name === 'error'}
									<span class="size-1.5 rounded-full bg-error"></span>
								{:else}
									<!-- wrench: a tool ran (pulsing until its result lands) -->
									<svg
										class="size-3 {step.result === undefined ? 'animate-pulse' : ''}"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										stroke-width="2"
										stroke-linecap="round"
										stroke-linejoin="round"
										aria-hidden="true"
									>
										<path
											d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"
										/>
									</svg>
								{/if}
								{step.name}
							</summary>
							<pre
								class="mono mt-1 max-h-40 overflow-auto rounded-xl border border-line bg-surface-2/60 p-2.5 text-[11px] whitespace-pre-wrap text-muted">{step.result ?? 'running…'}</pre>
						</details>
					{/each}
				</div>
			{/if}
			{#if message.text !== ''}
				<div
					class="markdown rounded-2xl rounded-tl-md border border-line bg-surface-2/60 px-4 py-3 {message.error
						? 'border-error/30 bg-error/10'
						: ''}"
				>
					{@html html}
					{#if live}
						<!-- streaming: a tool may be running (ml-1.5) or the model writing -->
						<span
							class="ml-1.5 inline-block size-3.5 animate-spin rounded-full border-2 border-accent border-t-transparent align-[-2px]"
							aria-hidden="true"
						></span>
					{/if}
				</div>
			{:else if live}
				<div
					class="inline-flex items-center gap-2.5 rounded-2xl rounded-tl-md border border-line bg-surface-2/60 px-4 py-3"
				>
					<span
						class="size-4 animate-spin rounded-full border-2 border-accent border-t-transparent"
						aria-hidden="true"
					></span>
					<span class="text-xs text-muted">Agent is working…</span>
				</div>
			{/if}
			{#if message.error}
				{#if message.text !== ''}
					<p class="text-xs text-error">
						The turn failed: {message.steps.find((s) => s.name === 'error')?.result ?? 'unknown error'}
					</p>
				{:else}
					<p class="text-xs text-error">
						The turn failed: {message.steps.find((s) => s.name === 'error')?.result ?? 'no answer — try again'}
					</p>
				{/if}
			{/if}
		</div>
	</div>
{/if}
