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
	/** Screenshots captured by the browser MCP, shown below the answer. */
	images?: string[];
	/** Set when the turn ended in an error event or a failed request. */
	error?: boolean;
}

export type StreamEvent = {
	type: 'session' | 'delta' | 'tool_call' | 'tool_result' | 'image' | 'done' | 'error';
	text?: string;
	name?: string;
	id?: string;
	result?: string;
	session_id?: string;
	message?: string;
	image_url?: string;
};

/** Conversations keep at most this many messages in storage. */
export const MAX_STORED_MESSAGES = 50;

/** Shown when a run finished without a word or a tool call. */
export const NO_ANSWER_TEXT = 'El modelo no produjo respuesta — inténtalo de nuevo.';

/** Per-turn streaming state that is not part of the message itself. */
export interface TurnState {
	/** A tool ran since the last text: the next text opens a new segment. */
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

/** The current assistant message of the turn (always the last one). */
function currentReply(messages: ChatMessage[]): ChatMessage {
	return messages[messages.length - 1] as ChatMessage;
}

/** Ensures the turn has a current assistant message to stream into. */
function ensureReply(messages: ChatMessage[]): ChatMessage {
	const last = messages[messages.length - 1];
	if (last && last.role === 'assistant') return last;
	const reply: ChatMessage = { role: 'assistant', text: '', steps: [] };
	messages.push(reply);
	return reply;
}

/**
 * Applies one SSE event to the streaming turn. Mutates messages in place (a
 * $state proxy in the app): when the agent produces text after running tools,
 * a new assistant message is started so each thought/step reads as its own
 * bubble instead of one long block.
 */
export function applyStreamEvent(
	messages: ChatMessage[],
	event: StreamEvent,
	turn: TurnState
): void {
	switch (event.type) {
		case 'session':
			if (event.session_id) turn.sessionId = event.session_id;
			break;
		case 'delta': {
			const text = event.text ?? '';
			if (text === '') break;
			let reply = ensureReply(messages);
			// A new round of text after tool work becomes its own message, so
			// the conversation reads as [thought] → [tools/thinking] → [thought].
			// Only split when the previous segment already said something; if it
			// only ran tools, the incoming text is this segment's answer.
			if (turn.toolSinceText && reply.text !== '') {
				reply.text = reply.text.replace(/\s+$/, '');
				const next: ChatMessage = { role: 'assistant', text: '', steps: [] };
				// Images produced by the tools of the previous segment usually
				// illustrate the answer that follows, so they move with it.
				if (reply.images?.length) {
					next.images = reply.images;
					delete reply.images;
				}
				messages.push(next);
				reply = next;
			}
			turn.toolSinceText = false;
			reply.text += text;
			break;
		}
		case 'tool_call': {
			const reply = ensureReply(messages);
			reply.steps.push({
				name: event.name ?? 'tool',
				id: event.id || undefined,
				start: Math.round(turn.elapsed())
			});
			turn.toolSinceText = true;
			break;
		}
		case 'tool_result': {
			// The matching call may live in a previous segment of this turn.
			for (let i = messages.length - 1; i >= 0; i--) {
				const m = messages[i];
				if (m.role !== 'assistant') break;
				const step = findOpenStep(m.steps, event);
				if (step) {
					step.end = Math.round(turn.elapsed());
					step.result = event.result ?? '';
					break;
				}
			}
			turn.toolSinceText = true;
			break;
		}
		case 'image': {
			// The backend only sends this once the PNG exists. A turn can
			// capture more than one site, so keep every image in order.
			if (!event.image_url) break;
			const reply = currentReply(messages);
			if (!reply || reply.role !== 'assistant') break;
			const images = reply.images ?? [];
			if (!images.includes(event.image_url)) {
				reply.images = [...images, event.image_url];
			}
			break;
		}
		case 'error': {
			turn.finished = true;
			const reply = ensureReply(messages);
			reply.error = true;
			reply.steps.push({ name: 'error', result: event.message ?? 'unknown error' });
			break;
		}
		case 'done': {
			turn.finished = true;
			const reply = ensureReply(messages);
			// A run that finished without answering leaves an empty bubble:
			// surface the failure instead of dropping it.
			if (reply.text === '' && reply.steps.length === 0) {
				reply.error = true;
				reply.text = NO_ANSWER_TEXT;
			}
			break;
		}
	}
}

/** Shown when the stream closes without a done or error event. */
export const CONNECTION_LOST_TEXT = 'la conexión se cerró antes de terminar la respuesta';

/** Fills a stopped run that had not said anything yet. */
export const STOPPED_TEXT = 'La ejecución se detuvo antes de terminar la respuesta.';

/**
 * Closes a turn the user stopped mid-run: the partial answer and its
 * steps stay as they are, because a stop is a decision, not a failure.
 */
export function endStoppedTurn(messages: ChatMessage[], turn: TurnState): void {
	if (turn.finished) return;
	turn.finished = true;
	const reply = currentReply(messages);
	if (!reply || reply.role !== 'assistant') return;
	if (reply.text === '' && reply.steps.length === 0) {
		reply.text = STOPPED_TEXT;
	}
}

/**
 * Closes a turn whose stream has ended. A stream that stops without a
 * terminal event (server restart, proxy cut, network drop) is a failure,
 * not a finished answer.
 */
export function endStream(messages: ChatMessage[], turn: TurnState): void {
	if (turn.finished) return;
	turn.finished = true;
	const reply = ensureReply(messages);
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
		m.steps.every((s) => typeof s === 'object' && s !== null && typeof s.name === 'string') &&
		(m.images === undefined ||
			(Array.isArray(m.images) && m.images.every((u) => typeof u === 'string')))
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
			return 'El scan sigue en marcha — el chat responde en cuanto el informe esté listo.';
		case 409:
			return 'Esta conversación sigue respondiendo otro mensaje (¿otra pestaña?) — espera a que acabe.';
		case 400:
			return 'El agente rechazó el mensaje (vacío o demasiado largo).';
		case 413:
			return 'El mensaje es demasiado largo para enviarlo.';
		case 422:
			return 'Esta conversación es demasiado larga — empieza una nueva.';
		case 429:
			return 'El asistente está ocupado con otros chats — prueba en un momento.';
		default:
			return `El endpoint del agente respondió ${status}.`;
	}
}
