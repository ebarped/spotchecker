/**
 * The agent state: the service info (enabled, model, suggestions), the tool
 * catalog for the sidebar, and the chat conversation with live streaming.
 * Pure SPA — every module only runs in the browser.
 */

export interface AgentTool {
	name: string;
	description: string;
}

export interface AgentToolSource {
	key: string;
	label: string;
	tools: AgentTool[];
	error?: string;
}

export interface AgentToolCatalog {
	builtin: AgentToolSource[];
	external: AgentToolSource[];
}

export interface AgentInfo {
	enabled: boolean;
	model: string;
	suggestions: string[];
}

import {
	applyStreamEvent,
	endStoppedTurn,
	endStream,
	httpErrorText,
	newTurnState,
	parseSSE,
	parseStoredChat,
	serializeChat,
	type ChatMessage,
	type StreamEvent,
	type TurnState
} from './agent-stream';

export type { ChatMessage, ToolStep } from './agent-stream';

export type ToolNodeCategory = 'user' | 'report' | 'external' | 'answer' | 'error';

/** One node of the vertical flow: the conversation read as a sequence. */
export interface FlowNode {
	kind: 'user' | 'tool' | 'answer' | 'error';
	text?: string;
	name?: string;
	category?: ToolNodeCategory;
	description?: string;
	duration?: number;
}

const CHAT_KEY = 'spotchecker:chat';

class AgentController {
	info = $state<AgentInfo | null>(null);
	catalog = $state<AgentToolCatalog | null>(null);
	catalogLoaded = $state(false);
	messages = $state<ChatMessage[]>([]);
	sessionId = $state('');
	streaming = $state(false);
	/** Aborts the fetch of the turn that is streaming right now. */
	private abort?: AbortController;

	/** Restores the last conversation from localStorage (F5 keeps the chat).
	 * Failed turns are dropped on restore as well as on save, so an old key
	 * written before that rule self-heals on the next reload. */
	restore(): void {
		try {
			const stored = parseStoredChat(localStorage.getItem(CHAT_KEY));
			if (!stored) return;
			this.messages = stored.messages;
			this.sessionId = stored.sessionId;
		} catch {
			// unavailable storage: start fresh
		}
	}

	/** Persists the conversation (see serializeChat for what survives). */
	private save(): void {
		try {
			localStorage.setItem(CHAT_KEY, serializeChat(this.sessionId, this.messages));
		} catch {
			// storage full or unavailable: the chat just does not persist
		}
	}


	/** Loads the service info; the tab only appears when it says enabled. */
	async loadInfo(): Promise<void> {
		this.restore();
		try {
			const res = await fetch('/api/agent/info');
			if (!res.ok) {
				this.info = null;
				return;
			}
			this.info = (await res.json()) as AgentInfo;
		} catch {
			this.info = null;
		}
	}

	/** Loads the tool catalog once, when the agent tab opens for the first time. */
	async loadCatalog(): Promise<void> {
		if (this.catalogLoaded) return;
		this.catalogLoaded = true;
		try {
			const res = await fetch('/api/agent/tools');
			if (!res.ok) return;
			this.catalog = (await res.json()) as AgentToolCatalog;
		} catch {
			this.catalogLoaded = false;
		}
	}

	/** Sends a user message and streams the assistant answer into state. */
	async send(text: string): Promise<void> {
		const message = text.trim();
		if (!message || this.streaming) return;

		this.messages.push({ role: 'user', text: message, steps: [] });
		this.messages.push({ role: 'assistant', text: '', steps: [] });
		// Re-read the assistant from the array: $state proxies objects pushed
		// into it, and mutating the raw original would never trigger the UI.
		const reply = this.messages[this.messages.length - 1] as ChatMessage;
		this.streaming = true;
		const t0 = performance.now();
		const turn: TurnState = newTurnState(() => performance.now() - t0);
		const abort = new AbortController();
		this.abort = abort;

		try {
			const res = await fetch('/api/agent/chat', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ session_id: this.sessionId || undefined, message }),
				signal: abort.signal
			});
			if (!res.ok || !res.body) {
				reply.text = httpErrorText(res.status);
				reply.error = true;
				return;
			}

			const reader = res.body.getReader();
			const decoder = new TextDecoder();
			let buffer = '';
			for (;;) {
				const { done, value } = await reader.read();
				if (done) break;
				const parsed = parseSSE(buffer + decoder.decode(value, { stream: true }));
				buffer = parsed.rest;
				for (const event of parsed.events) this.apply(reply, event, turn);
			}
			endStream(reply, turn);
		} catch (error) {
			if (abort.signal.aborted) {
				endStoppedTurn(reply, turn);
			} else {
				reply.error = true;
				if (reply.text === '') {
					reply.text = `The chat failed: ${error instanceof Error ? error.message : String(error)}`;
				}
			}
		} finally {
			this.abort = undefined;
			this.streaming = false;
			this.save();
		}
	}

	/** Stops the running turn: the server cancels the agent on disconnect. */
	stop(): void {
		this.abort?.abort();
	}

	/** Applies one SSE event to the streaming reply and the session id. */
	private apply(reply: ChatMessage, event: StreamEvent, turn: TurnState): void {
		applyStreamEvent(reply, event, turn);
		if (turn.sessionId && turn.sessionId !== this.sessionId) this.sessionId = turn.sessionId;
	}

	/** Starts a fresh conversation. */
	reset(): void {
		this.messages = [];
		this.sessionId = '';
		try {
			localStorage.removeItem(CHAT_KEY);
		} catch {
			// storage unavailable: nothing to clear
		}
	}

	/** Which origin a tool belongs to, per the loaded catalog. */
	categoryOf(name: string): ToolNodeCategory {
		const reportTools = [
			'get_report_summary',
			'list_establishments',
			'get_establishment_detail'
		];
		if (reportTools.includes(name)) return 'report';
		if (this.catalog?.builtin.some((s) => s.tools.some((t) => t.name === name))) return 'report';
		return 'external';
	}

	/**
	 * The whole conversation as a vertical flow: user question, its tool
	 * calls in order, the answer, the next question… exactly the order the
	 * agent traversed.
	 */
	flow = $derived.by(() => {
		const descriptions = new Map<string, string>();
		for (const source of [...(this.catalog?.builtin ?? []), ...(this.catalog?.external ?? [])]) {
			for (const tool of source.tools) descriptions.set(tool.name, tool.description);
		}

		const nodes: FlowNode[] = [];
		for (const message of this.messages) {
			if (message.role === 'user') {
				nodes.push({ kind: 'user', text: message.text });
				continue;
			}
			for (const step of message.steps) {
				const kind = step.name === 'error' ? ('error' as const) : ('tool' as const);
				nodes.push({
					kind,
					name: step.name,
					category: step.name === 'error' ? 'error' : this.categoryOf(step.name),
					description: descriptions.get(step.name),
					duration:
						step.start !== undefined && step.end !== undefined ? step.end - step.start : undefined
				});
			}
			if (message.text !== '') {
				nodes.push({ kind: 'answer', text: message.text });
			}
		}
		return nodes;
	});
}

export const agent = new AgentController();
