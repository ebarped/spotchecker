/**
 * The pure core of the agent chat: how the SSE stream of /api/agent/chat
 * becomes an assistant message, and how a conversation is persisted. No
 * Svelte runes and no browser globals, so every rule here is unit-tested
 * (agent-stream.test.ts); agent.svelte.ts only wires it to state.
 */

export interface ToolStep {
	name: string;
	/** Tool-call id from the backend, pairing the result with its call. */
	id?: string;
	result?: string;
	/** Milliseconds since the turn started, captured live from the SSE
	 * stream; restored conversations fall back to sequential slots. */
	start?: number;
	end?: number;
}

export interface ChatMessage {
	role: 'user' | 'assistant';
	text: string;
	steps: ToolStep[];
	/** Set when the turn ended in an error event or a failed request. */
	error?: boolean;
}

export type StreamEvent = {
	type: 'session' | 'delta' | 'tool_call' | 'tool_result' | 'done' | 'error';
	text?: string;
	name?: string;
	id?: string;
	result?: string;
	session_id?: string;
	message?: string;
};

/** Conversations keep at most this many messages in storage. */
export const MAX_STORED_MESSAGES = 50;

/** Shown when a run finished without a word or a tool call. */
export const NO_ANSWER_TEXT = 'The model produced no answer — try again.';

/** Per-turn streaming state that is not part of the message itself. */
export interface TurnState {
	/** A tool ran since the last text: the next text opens a new paragraph. */
	toolSinceText: boolean;
	/** Milliseconds since the turn started. */
	elapsed: () => number;
	/** Session id announced by the server, when any. */
	sessionId?: string;
	/** A terminal event (done or error) arrived. */
	finished: boolean;
}

export function newTurnState(elapsed: () => number): TurnState {
	return { toolSinceText: false, elapsed, finished: false };
}

/**
 * Applies one SSE event to the streaming assistant message. Mutates reply
 * in place (it is a $state proxy in the app) and the turn state.
 */
export function applyStreamEvent(reply: ChatMessage, event: StreamEvent, turn: TurnState): void {
	switch (event.type) {
		case 'session':
			if (event.session_id) turn.sessionId = event.session_id;
			break;
		case 'delta': {
			const text = event.text ?? '';
			if (text === '') break;
			// Each model round between tool calls is its own thought: without
			// a break they glue together ("…issues.The findings…").
			if (turn.toolSinceText && reply.text !== '') {
				reply.text = reply.text.replace(/\s+$/, '') + '\n\n';
			}
			turn.toolSinceText = false;
			reply.text += text;
			break;
		}
		case 'tool_call':
			reply.steps.push({
				name: event.name ?? 'tool',
				id: event.id || undefined,
				start: Math.round(turn.elapsed())
			});
			turn.toolSinceText = true;
			break;
		case 'tool_result': {
			const step = findOpenStep(reply.steps, event);
			if (step) {
				step.end = Math.round(turn.elapsed());
				step.result = event.result ?? '';
			}
			// Text after a result is a new round even when the text of the
			// previous one arrived between the call and its result.
			turn.toolSinceText = true;
			break;
		}
		case 'error':
			turn.finished = true;
			reply.error = true;
			reply.steps.push({ name: 'error', result: event.message ?? 'unknown error' });
			break;
		case 'done':
			turn.finished = true;
			// A run that finished without answering leaves an empty bubble:
			// surface the failure instead of dropping it.
			if (reply.text === '' && reply.steps.length === 0) {
				reply.error = true;
				reply.text = NO_ANSWER_TEXT;
			}
			break;
	}
}

/** Shown when the stream closes without a done or error event. */
export const CONNECTION_LOST_TEXT = 'the connection closed before the answer finished';

/** Fills a stopped run that had not said anything yet. */
export const STOPPED_TEXT = 'The run was stopped before the answer finished.';

/**
 * Closes a turn the user stopped mid-run: the partial answer and its
 * steps stay as they are, because a stop is a decision, not a failure.
 */
export function endStoppedTurn(reply: ChatMessage, turn: TurnState): void {
	if (turn.finished) return;
	turn.finished = true;
	if (reply.text === '' && reply.steps.length === 0) {
		reply.text = STOPPED_TEXT;
	}
}

/**
 * Closes a turn whose stream has ended. A stream that stops without a
 * terminal event (server restart, proxy cut, network drop) is a failure,
 * not a finished answer.
 */
export function endStream(reply: ChatMessage, turn: TurnState): void {
	if (turn.finished) return;
	turn.finished = true;
	reply.error = true;
	reply.steps.push({ name: 'error', result: CONNECTION_LOST_TEXT });
}

/**
 * The step a tool_result belongs to. By call id when the backend sent one —
 * the same tool can run several times in a turn and results may arrive in
 * any order; otherwise the first still-open call of that name.
 */
export function findOpenStep(steps: ToolStep[], event: StreamEvent): ToolStep | undefined {
	if (event.id) {
		const byId = steps.find((s) => s.id === event.id);
		if (byId) return byId;
	}
	return steps.find(
		(s) => s.name === event.name && s.result === undefined && (!event.id || !s.id)
	);
}

/**
 * Splits buffered SSE text into complete events and the unfinished rest.
 * Frames end with a blank line; only `data:` lines carry payloads, and a
 * malformed payload is skipped rather than killing the stream.
 */
export function parseSSE(buffer: string): { events: StreamEvent[]; rest: string } {
	const events: StreamEvent[] = [];
	const normalized = buffer.replace(/\r\n/g, '\n');
	let start = 0;
	for (;;) {
		const boundary = normalized.indexOf('\n\n', start);
		if (boundary < 0) break;
		const frame = normalized.slice(start, boundary);
		start = boundary + 2;
		for (const line of frame.split('\n')) {
			if (!line.startsWith('data:')) continue;
			try {
				const event = JSON.parse(line.slice(5).trimStart()) as StreamEvent;
				if (event && typeof event.type === 'string') events.push(event);
			} catch {
				// a corrupt frame must not take the rest of the answer with it
			}
		}
	}
	return { events, rest: normalized.slice(start) };
}

/** Runtime shape guard for messages restored from storage. */
export function isChatMessage(value: unknown): value is ChatMessage {
	if (typeof value !== 'object' || value === null) return false;
	const m = value as Partial<ChatMessage>;
	return (
		(m.role === 'user' || m.role === 'assistant') &&
		typeof m.text === 'string' &&
		Array.isArray(m.steps) &&
		m.steps.every((s) => typeof s === 'object' && s !== null && typeof s.name === 'string')
	);
}

/**
 * Drops failed turns (the question and its failed answer): their error text
 * is a ghost that reads as a live failure forever after a reload.
 */
export function dropFailedTurns(messages: ChatMessage[]): ChatMessage[] {
	const keep: ChatMessage[] = [];
	for (const message of messages) {
		if (message.role === 'assistant' && message.error) {
			if (keep.at(-1)?.role === 'user') keep.pop();
			continue;
		}
		keep.push(message);
	}
	return keep;
}

/** Serializes a conversation for storage: failed turns out, last N kept. */
export function serializeChat(sessionId: string, messages: ChatMessage[]): string {
	return JSON.stringify({
		session_id: sessionId,
		messages: dropFailedTurns(messages).slice(-MAX_STORED_MESSAGES)
	});
}

/** Restores a stored conversation; null when there is nothing usable. */
export function parseStoredChat(
	raw: string | null
): { sessionId: string; messages: ChatMessage[] } | null {
	if (!raw) return null;
	try {
		const data = JSON.parse(raw) as { session_id?: unknown; messages?: unknown };
		if (!data || !Array.isArray(data.messages)) return null;
		const messages = dropFailedTurns(data.messages.filter(isChatMessage));
		if (messages.length === 0) return null;
		return {
			sessionId: typeof data.session_id === 'string' ? data.session_id : '',
			messages
		};
	} catch {
		return null;
	}
}

/** Text shown when the chat endpoint refuses the request before streaming. */
export function httpErrorText(status: number): string {
	switch (status) {
		case 503:
			return 'The scan is still running — the chat answers as soon as the report is ready.';
		case 409:
			return 'This conversation is still answering another message (another tab?) — wait for it to finish.';
		case 400:
			return 'The agent rejected the message (empty or too long).';
		case 413:
			return 'The message is too large to send.';
		case 422:
			return 'This conversation is too long — start a new chat.';
		case 429:
			return 'The assistant is busy with other chats — try again in a moment.';
		default:
			return `The agent endpoint answered ${status}.`;
	}
}
