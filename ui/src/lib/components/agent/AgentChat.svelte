<script lang="ts">
	import { agent } from '../../agent.svelte';
	import AgentMessage from './AgentMessage.svelte';

	let { ready }: { ready: boolean } = $props();

	let scroller: HTMLDivElement | undefined = $state();
	let draft = $state('');
	let copied = $state(false);
	let textareaEl: HTMLTextAreaElement | undefined = $state();

	async function copySession() {
		try {
			await navigator.clipboard.writeText(agent.sessionId);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			// clipboard unavailable: the id is visible anyway
		}
	}

	// Keep the newest content in view while the answer streams in.
	$effect(() => {
		void agent.messages.length;
		void agent.messages.at(-1)?.text.length;
		if (scroller) scroller.scrollTop = scroller.scrollHeight;
	});

	// When a turn ends, hand the focus back so the next question flows.
	$effect(() => {
		if (!agent.streaming) textareaEl?.focus();
	});

	function submitNow() {
		const text = draft;
		if (!text.trim() || agent.streaming) return;
		draft = '';
		void agent.send(text);
		textareaEl?.focus();
	}

	function submit(event: SubmitEvent) {
		event.preventDefault();
		submitNow();
	}

	function keydown(event: KeyboardEvent) {
		// Enter sends; Shift+Enter inserts a newline.
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			submitNow();
		}
	}

	function suggest(text: string) {
		void agent.send(text);
	}
</script>

<section class="glass flex h-[72vh] flex-col rounded-2xl">
	<header class="flex items-center justify-between border-b border-line px-4 py-2.5">
		<div class="flex items-center gap-2">
			<span class="text-sm font-semibold">Agent</span>
			{#if agent.info}
				<span class="mono rounded-md bg-accent/10 px-1.5 py-0.5 text-[11px] font-medium text-accent">
					{agent.info.model}
				</span>
			{/if}
		</div>
		<div class="flex items-center gap-3">
			{#if agent.sessionId}
				<button
					type="button"
					class="mono hidden whitespace-nowrap px-1 text-[11px] text-faint transition-colors hover:text-muted sm:block"
					title="Click to copy the session id"
					onclick={copySession}
				>
					{copied ? 'session_id: copied!' : `session_id: ${agent.sessionId}`}
				</button>
			{/if}
		</div>
	</header>

	<div bind:this={scroller} class="fade-y flex-1 space-y-4 overflow-y-auto px-4 py-4">
		{#if agent.messages.length === 0}
			<div class="mx-auto max-w-md pt-10 text-center">
				<p class="text-sm text-muted">
					Ask anything about the inspection report: check an establishment's score, see which
					signals support it, or summarize the visit queue.
				</p>
				{#if agent.info && agent.info.suggestions.length > 0}
					<div class="mt-4 flex flex-wrap justify-center gap-2">
						{#each agent.info.suggestions as suggestion (suggestion)}
							<button
								type="button"
								class="rounded-full border border-accent/40 bg-accent/10 px-3 py-1 text-xs font-medium text-accent transition-colors hover:bg-accent/20"
								onclick={() => suggest(suggestion)}
							>
								{suggestion}
							</button>
						{/each}
					</div>
				{/if}
			</div>
		{:else}
			{#each agent.messages as message, index (index)}
				<AgentMessage
					{message}
					live={agent.streaming && index === agent.messages.length - 1 && message.role === 'assistant'}
				/>
			{/each}
		{/if}
	</div>

	<form onsubmit={submit} class="flex items-stretch gap-2 border-t border-line px-4 py-3">
		<textarea
			bind:this={textareaEl}
			bind:value={draft}
			rows="2"
			placeholder={ready
				? 'Ask the agent to investigate…'
				: 'The scan is still running — the chat answers as soon as the report is ready.'}
			disabled={!ready}
			onkeydown={keydown}
			class="min-w-0 flex-1 resize-none rounded-xl border border-line bg-surface px-3 py-2 text-sm text-ink placeholder:text-faint focus:border-line-strong focus:outline-none"
		></textarea>
		<div class="flex w-28 flex-col gap-1.5">
			<button
				type="submit"
				disabled={!ready || agent.streaming || draft.trim() === ''}
				class="tile flex-[2] rounded-xl text-sm font-semibold text-white disabled:opacity-40"
				style="--tile: #b85c38"
			>
				Send
			</button>
			<button
				type="button"
				onclick={() => agent.reset()}
				disabled={agent.streaming || agent.messages.length === 0}
				class="flex-1 rounded-xl border border-accent/40 bg-accent/10 text-[11px] font-semibold text-accent transition-colors hover:bg-accent/20 disabled:opacity-40"
			>
				New chat
			</button>
		</div>
	</form>
</section>
