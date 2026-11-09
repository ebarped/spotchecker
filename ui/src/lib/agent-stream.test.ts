import { describe, expect, it } from 'vitest';
import {
	CONNECTION_LOST_TEXT,
	MAX_STORED_MESSAGES,
	NO_ANSWER_TEXT,
	STOPPED_TEXT,
	applyStreamEvent,
	dropFailedTurns,
	endStoppedTurn,
	endStream,
	findOpenStep,
	httpErrorText,
	isChatMessage,
	newTurnState,
	parseSSE,
	parseStoredChat,
	serializeChat,
	type ChatMessage,
	type StreamEvent
} from './agent-stream';

function reply(): ChatMessage {
	return { role: 'assistant', text: '', steps: [] };
}

/** Feeds events through a fresh turn whose clock ticks 10ms per read. */
function run(events: StreamEvent[]) {
	let clock = 0;
	const turn = newTurnState(() => (clock += 10));
	const message = reply();
	for (const event of events) applyStreamEvent(message, event, turn);
	return { message, turn };
}

describe('applyStreamEvent — text', () => {
	it('concatenates deltas of one round', () => {
		const { message } = run([
			{ type: 'delta', text: 'Hel' },
			{ type: 'delta', text: 'lo' }
		]);
		expect(message.text).toBe('Hello');
	});

	it('opens a new paragraph when text follows a tool call', () => {
		const { message } = run([
			{ type: 'delta', text: 'Looking at the summary.' },
			{ type: 'tool_call', name: 'get_report_summary', id: 'a' },
			{ type: 'tool_result', name: 'get_report_summary', id: 'a', result: '{}' },
			{ type: 'delta', text: 'The visit queue is dominated by two establishments.' }
		]);
		expect(message.text).toBe(
			'Looking at the summary.\n\nThe visit queue is dominated by two establishments.'
		);
	});

	it('does not stack breaks or trailing spaces', () => {
		const { message } = run([
			{ type: 'delta', text: 'First.  \n' },
			{ type: 'tool_call', name: 't', id: '1' },
			{ type: 'tool_call', name: 't', id: '2' },
			{ type: 'delta', text: 'Second.' }
		]);
		expect(message.text).toBe('First.\n\nSecond.');
	});

	it('does not start the answer with a break when a tool runs first', () => {
		const { message } = run([
			{ type: 'tool_call', name: 't', id: '1' },
			{ type: 'delta', text: 'Answer.' }
		]);
		expect(message.text).toBe('Answer.');
	});

	it('separates rounds when the text sits between a call and its result', () => {
		const { message } = run([
			{ type: 'tool_call', name: 't', id: '1' },
			{ type: 'delta', text: 'Round one.' },
			{ type: 'tool_result', name: 't', id: '1', result: '{}' },
			{ type: 'delta', text: '## Verdict' }
		]);
		expect(message.text).toBe('Round one.\n\n## Verdict');
	});

	it('ignores empty deltas without consuming the pending break', () => {
		const { message } = run([
			{ type: 'delta', text: 'A.' },
			{ type: 'tool_call', name: 't', id: '1' },
			{ type: 'delta', text: '' },
			{ type: 'delta', text: 'B.' }
		]);
		expect(message.text).toBe('A.\n\nB.');
	});
});

describe('applyStreamEvent — tool steps', () => {
	it('records each call with its id and start time', () => {
		const { message } = run([{ type: 'tool_call', name: 'list_findings', id: 'call_1' }]);
		expect(message.steps).toEqual([{ name: 'list_findings', id: 'call_1', start: 10 }]);
	});

	it('pairs results to calls by id even out of order', () => {
		const { message } = run([
			{ type: 'tool_call', name: 'get_query_detail', id: 'q1' },
			{ type: 'tool_call', name: 'get_query_detail', id: 'q2' },
			{ type: 'tool_call', name: 'get_query_detail', id: 'q3' },
			{ type: 'tool_result', name: 'get_query_detail', id: 'q3', result: 'third' },
			{ type: 'tool_result', name: 'get_query_detail', id: 'q1', result: 'first' },
			{ type: 'tool_result', name: 'get_query_detail', id: 'q2', result: 'second' }
		]);
		expect(message.steps.map((s) => [s.id, s.result])).toEqual([
			['q1', 'first'],
			['q2', 'second'],
			['q3', 'third']
		]);
		for (const step of message.steps) expect(step.end).toBeGreaterThan(step.start!);
	});

	it('falls back to the first open call of that name without ids', () => {
		const { message } = run([
			{ type: 'tool_call', name: 'list_findings' },
			{ type: 'tool_call', name: 'list_findings' },
			{ type: 'tool_result', name: 'list_findings', result: 'one' },
			{ type: 'tool_result', name: 'list_findings', result: 'two' }
		]);
		expect(message.steps.map((s) => s.result)).toEqual(['one', 'two']);
	});

	it('does not hand a result with an id to a call that has another id', () => {
		const steps = [{ name: 't', id: 'a' }];
		expect(findOpenStep(steps, { type: 'tool_result', name: 't', id: 'zzz' })).toBeUndefined();
	});

	it('drops a result that matches no call', () => {
		const { message } = run([{ type: 'tool_result', name: 'ghost', id: 'x', result: 'r' }]);
		expect(message.steps).toEqual([]);
	});

	it('keeps an empty result distinct from a pending one', () => {
		const { message } = run([
			{ type: 'tool_call', name: 't', id: '1' },
			{ type: 'tool_result', name: 't', id: '1' }
		]);
		expect(message.steps[0].result).toBe('');
	});
});

describe('applyStreamEvent — terminal events', () => {
	it('records the session id', () => {
		const { turn } = run([{ type: 'session', session_id: 'abc' }]);
		expect(turn.sessionId).toBe('abc');
	});

	it('marks errors and keeps the partial text', () => {
		const { message, turn } = run([
			{ type: 'delta', text: 'Partial.' },
			{ type: 'error', message: 'the chat timed out' }
		]);
		expect(message.error).toBe(true);
		expect(message.text).toBe('Partial.');
		expect(message.steps.at(-1)).toEqual({ name: 'error', result: 'the chat timed out' });
		expect(turn.finished).toBe(true);
	});

	it('turns a silent done into a visible failure', () => {
		const { message } = run([{ type: 'done' }]);
		expect(message.error).toBe(true);
		expect(message.text).toBe(NO_ANSWER_TEXT);
	});

	it('leaves a normal done alone', () => {
		const { message } = run([{ type: 'delta', text: 'ok' }, { type: 'done' }]);
		expect(message.error).toBeUndefined();
	});

	it('endStream flags a stream cut without a terminal event', () => {
		const { message, turn } = run([{ type: 'delta', text: 'half an ans' }]);
		endStream(message, turn);
		expect(message.error).toBe(true);
		expect(message.steps.at(-1)?.result).toBe(CONNECTION_LOST_TEXT);
	});

	it('endStream is a no-op after done or error', () => {
		const { message, turn } = run([{ type: 'delta', text: 'ok' }, { type: 'done' }]);
		endStream(message, turn);
		expect(message.error).toBeUndefined();
		expect(message.steps).toEqual([]);
	});
});

describe('endStoppedTurn', () => {
	it('keeps the partial answer without marking it a failure', () => {
		const { message, turn } = run([{ type: 'delta', text: 'Half an ans' }]);
		endStoppedTurn(message, turn);
		expect(message.error).toBeUndefined();
		expect(message.text).toBe('Half an ans');
		expect(turn.finished).toBe(true);
	});

	it('fills an empty run with the stopped text instead of a blank bubble', () => {
		const { message, turn } = run([]);
		endStoppedTurn(message, turn);
		expect(message.error).toBeUndefined();
		expect(message.text).toBe(STOPPED_TEXT);
	});

	it('is a no-op after a terminal event', () => {
		const { message, turn } = run([{ type: 'delta', text: 'ok' }, { type: 'done' }]);
		endStoppedTurn(message, turn);
		expect(message.text).toBe('ok');
	});
});

describe('parseSSE', () => {
	it('parses complete frames and keeps the rest', () => {
		const { events, rest } = parseSSE(
			'data: {"type":"delta","text":"a"}\n\ndata: {"type":"done"}\n\ndata: {"type":"del'
		);
		expect(events.map((e) => e.type)).toEqual(['delta', 'done']);
		expect(rest).toBe('data: {"type":"del');
	});

	it('reassembles an event split across chunks', () => {
		const first = parseSSE('data: {"type":"delta",');
		expect(first.events).toEqual([]);
		const second = parseSSE(first.rest + '"text":"x"}\n\n');
		expect(second.events).toEqual([{ type: 'delta', text: 'x' }]);
		expect(second.rest).toBe('');
	});

	it('accepts CRLF framing and data: without a space', () => {
		const { events } = parseSSE('data:{"type":"done"}\r\n\r\n');
		expect(events).toEqual([{ type: 'done' }]);
	});

	it('skips a corrupt frame and keeps the following ones', () => {
		const { events } = parseSSE('data: {not json\n\ndata: {"type":"done"}\n\n');
		expect(events).toEqual([{ type: 'done' }]);
	});

	it('ignores comments and non-data fields', () => {
		const { events } = parseSSE(': ping\nevent: x\ndata: {"type":"done"}\n\n');
		expect(events).toEqual([{ type: 'done' }]);
	});
});

describe('persistence', () => {
	const user = (text: string): ChatMessage => ({ role: 'user', text, steps: [] });
	const answer = (text: string, error?: boolean): ChatMessage => ({
		role: 'assistant',
		text,
		steps: [],
		...(error ? { error } : {})
	});

	it('drops failed turns with their question', () => {
		const kept = dropFailedTurns([user('q1'), answer('a1'), user('q2'), answer('boom', true)]);
		expect(kept).toEqual([user('q1'), answer('a1')]);
	});

	it('drops an orphan failed answer', () => {
		expect(dropFailedTurns([answer('boom', true), user('q'), answer('a')])).toEqual([
			user('q'),
			answer('a')
		]);
	});

	it('serializes the session and only the last messages', () => {
		const many: ChatMessage[] = [];
		for (let i = 0; i < MAX_STORED_MESSAGES; i++) many.push(user(`q${i}`), answer(`a${i}`));
		const stored = JSON.parse(serializeChat('sid', many));
		expect(stored.session_id).toBe('sid');
		expect(stored.messages).toHaveLength(MAX_STORED_MESSAGES);
		expect(stored.messages.at(-1).text).toBe(`a${MAX_STORED_MESSAGES - 1}`);
	});

	it('round-trips a conversation', () => {
		const messages = [user('q'), { ...answer('a'), steps: [{ name: 't', id: '1', result: 'r' }] }];
		expect(parseStoredChat(serializeChat('sid', messages))).toEqual({ sessionId: 'sid', messages });
	});

	it('heals stored failed turns written by older builds', () => {
		const raw = JSON.stringify({ session_id: 's', messages: [user('q'), answer('x', true)] });
		expect(parseStoredChat(raw)).toBeNull();
	});

	it('rejects garbage', () => {
		expect(parseStoredChat(null)).toBeNull();
		expect(parseStoredChat('not json')).toBeNull();
		expect(parseStoredChat('{"messages":"nope"}')).toBeNull();
		expect(parseStoredChat('null')).toBeNull();
	});

	it('filters malformed messages and tolerates a missing session id', () => {
		const raw = JSON.stringify({ messages: [user('q'), { role: 'robot' }, answer('a')] });
		expect(parseStoredChat(raw)).toEqual({ sessionId: '', messages: [user('q'), answer('a')] });
	});

	it('isChatMessage checks the step shape', () => {
		expect(isChatMessage({ role: 'user', text: '', steps: [{ name: 'x' }] })).toBe(true);
		expect(isChatMessage({ role: 'user', text: '', steps: [{}] })).toBe(false);
		expect(isChatMessage({ role: 'user', text: 1, steps: [] })).toBe(false);
	});
});

describe('httpErrorText', () => {
	it('explains the statuses the chat endpoint uses', () => {
		expect(httpErrorText(503)).toMatch(/scan sigue en marcha/);
		expect(httpErrorText(409)).toMatch(/sigue respondiendo/);
		expect(httpErrorText(400)).toMatch(/demasiado largo/);
		expect(httpErrorText(413)).toMatch(/demasiado largo/);
		expect(httpErrorText(422)).toMatch(/empieza una nueva/);
		expect(httpErrorText(429)).toMatch(/ocupado/);
		expect(httpErrorText(500)).toBe('El endpoint del agente respondió 500.');
	});
});
