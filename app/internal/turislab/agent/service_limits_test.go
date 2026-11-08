package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/v2/model"
)

// post sends a chat request and returns the recorder, whatever the status.
func post(svc *Service, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	svc.HandleChat(rec, httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(body)))
	return rec
}

// gatedModel blocks inside GenerateContent until the test opens the gate,
// which holds a turn "mid-answer" so concurrent requests can be tried
// against it.
type gatedModel struct {
	started chan struct{}
	gate    chan struct{}
}

func newGatedModel() *gatedModel {
	return &gatedModel{started: make(chan struct{}, 16), gate: make(chan struct{})}
}

func (m *gatedModel) Name() string { return "gated" }

func (m *gatedModel) GenerateContent(ctx context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		m.started <- struct{}{}
		select {
		case <-m.gate:
			yield(textResponse("released", false), nil)
		case <-ctx.Done():
			yield(nil, ctx.Err())
		}
	}
}

func gatedService(t *testing.T, m *gatedModel) *Service {
	t.Helper()
	svc := testService(t, &scriptedModel{}, fixtureReport())
	svc.modelFactory = func(context.Context) (adkmodel.LLM, error) { return m, nil }
	return svc
}

func waitStarted(t *testing.T, m *gatedModel) {
	t.Helper()
	select {
	case <-m.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("the turn never reached the model")
	}
}

func TestHandleChatMessageLength(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{{textResponse("ok", false)}}}
	svc := testService(t, model, fixtureReport())

	long := post(svc, fmt.Sprintf(`{"message":%q}`, strings.Repeat("a", maxMessageChars+1)))
	if long.Code != http.StatusBadRequest || !strings.Contains(long.Body.String(), "longer than") {
		t.Errorf("over-long message: %d %s, want 400 with the limit", long.Code, long.Body.String())
	}
	if model.requestCount() != 0 {
		t.Errorf("the model saw a refused message")
	}

	// The limit counts characters, not bytes: 8000 accented letters are 16KB.
	exact := post(svc, fmt.Sprintf(`{"message":%q}`, strings.Repeat("é", maxMessageChars)))
	if exact.Code != http.StatusOK {
		t.Errorf("message of exactly %d runes: status %d, want 200", maxMessageChars, exact.Code)
	}
}

func TestHandleChatRejectsABadSessionID(t *testing.T) {
	model := &scriptedModel{}
	svc := testService(t, model, fixtureReport())
	for _, id := range []string{"short", "../../etc/passwd", "has space in id", "line\\nbreak-1234", strings.Repeat("a", 65), "<script>alert(1)</script>"} {
		rec := post(svc, fmt.Sprintf(`{"session_id":%q,"message":"hi"}`, id))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid session_id") {
			t.Errorf("session_id %q: %d %s, want 400 invalid session_id", id, rec.Code, rec.Body.String())
		}
	}
	if model.requestCount() != 0 {
		t.Errorf("the model was called for a refused session id")
	}
	if svc.registry.size() != 0 {
		t.Errorf("a refused session id was registered")
	}
}

func TestHandleChatRejectsAnOversizedBody(t *testing.T) {
	model := &scriptedModel{}
	svc := testService(t, model, fixtureReport())
	body := `{"message":"` + strings.Repeat("a", maxRequestBytes) + `"}`
	rec := post(svc, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d, want 413", rec.Code)
	}
	if model.requestCount() != 0 {
		t.Errorf("the model was called for an oversized body")
	}
}

func TestHandleChatRejectsMalformedBodies(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	for _, body := range []string{``, `{}`, `[]`, `{"message":123}`, `{"message":null}`} {
		if rec := post(svc, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, rec.Code)
		}
	}
}

func TestHandleChatSessionBusy(t *testing.T) {
	gated := newGatedModel()
	svc := gatedService(t, gated)
	const id = "0123456789abcdef0123456789abcdef"

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- post(svc, fmt.Sprintf(`{"session_id":%q,"message":"first"}`, id)) }()
	waitStarted(t, gated)

	second := post(svc, fmt.Sprintf(`{"session_id":%q,"message":"second"}`, id))
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "already answering") {
		t.Errorf("concurrent turn: %d %s, want 409", second.Code, second.Body.String())
	}

	// A different conversation is not blocked by the busy one.
	other := make(chan *httptest.ResponseRecorder, 1)
	go func() { other <- post(svc, `{"message":"unrelated"}`) }()
	waitStarted(t, gated)

	close(gated.gate)
	for _, ch := range []chan *httptest.ResponseRecorder{first, other} {
		if rec := <-ch; rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"type":"done"`) {
			t.Errorf("a running turn did not finish cleanly: %d %s", rec.Code, rec.Body.String())
		}
	}

	// Released: the same conversation takes a new turn.
	gated2 := newGatedModel()
	close(gated2.gate)
	svc.modelFactory = func(context.Context) (adkmodel.LLM, error) { return gated2, nil }
	if rec := post(svc, fmt.Sprintf(`{"session_id":%q,"message":"third"}`, id)); rec.Code != http.StatusOK {
		t.Errorf("turn after the first finished: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatReleasesTheSessionAfterAFailedTurn(t *testing.T) {
	model := &scriptedModel{} // exhausted: every round errors
	svc := testService(t, model, fixtureReport())
	const id = "0123456789abcdef0123456789abcdef"
	body := fmt.Sprintf(`{"session_id":%q,"message":"hi"}`, id)
	for range 2 {
		rec := post(svc, body)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"type":"error"`) {
			t.Fatalf("failed turn: %d %s, want an error event", rec.Code, rec.Body.String())
		}
	}
}

func TestHandleChatConcurrencyCap(t *testing.T) {
	gated := newGatedModel()
	svc := gatedService(t, gated)

	done := make(chan *httptest.ResponseRecorder, maxConcurrentChats)
	for range maxConcurrentChats {
		go func() { done <- post(svc, `{"message":"hold"}`) }()
	}
	for range maxConcurrentChats {
		waitStarted(t, gated)
	}

	rec := post(svc, `{"message":"one too many"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("over the cap: %d (Retry-After %q), want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	if svc.registry.size() != maxConcurrentChats {
		t.Errorf("a refused chat was registered: %d conversations", svc.registry.size())
	}

	close(gated.gate)
	for range maxConcurrentChats {
		if r := <-done; r.Code != http.StatusOK {
			t.Errorf("a held turn failed: %d", r.Code)
		}
	}
	// The slots came back.
	gated2 := newGatedModel()
	close(gated2.gate)
	svc.modelFactory = func(context.Context) (adkmodel.LLM, error) { return gated2, nil }
	if r := post(svc, `{"message":"after"}`); r.Code != http.StatusOK {
		t.Errorf("after the others finished: %d, want 200", r.Code)
	}
}

func TestHandleChatSessionTurnCap(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{textResponse("one", false)}, {textResponse("two", false)},
	}}
	svc := testService(t, model, fixtureReport())
	svc.registry.maxTurn = 2
	const id = "0123456789abcdef0123456789abcdef"
	body := fmt.Sprintf(`{"session_id":%q,"message":"q"}`, id)

	for i := range 2 {
		if rec := post(svc, body); rec.Code != http.StatusOK {
			t.Fatalf("turn %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := post(svc, body)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "start a new chat") {
		t.Errorf("third turn: %d %s, want 422 telling to start a new chat", rec.Code, rec.Body.String())
	}
	if model.requestCount() != 2 {
		t.Errorf("the model ran for the refused turn")
	}
}

func TestHandleChatUnknownSessionStartsFresh(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{{textResponse("hello", false)}}}
	svc := testService(t, model, fixtureReport())
	const id = "deadbeefdeadbeefdeadbeefdeadbeef" // e.g. from before a restart

	events := chat(t, svc, fmt.Sprintf(`{"session_id":%q,"message":"remember me?"}`, id))
	if events[0].SessionID != id {
		t.Errorf("session id = %q, want the client's", events[0].SessionID)
	}
	if last := events[len(events)-1]; last.Type != "done" {
		t.Errorf("last event = %+v, want done", last)
	}
	if n := len(model.requests[0].Contents); n != 1 {
		t.Errorf("a fresh session sent %d history entries, want just the question", n)
	}
}

func TestHandleChatEvictedConversationLosesItsHistory(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{textResponse("answer A", false)},
		{textResponse("answer B", false)},
		{textResponse("answer A again", false)},
	}}
	svc := testService(t, model, fixtureReport())
	svc.registry.max = 1
	const a, b = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	chat(t, svc, fmt.Sprintf(`{"session_id":%q,"message":"secret from A"}`, a))
	chat(t, svc, fmt.Sprintf(`{"session_id":%q,"message":"question B"}`, b)) // evicts A
	if svc.registry.size() != 1 {
		t.Fatalf("registry holds %d conversations, want 1", svc.registry.size())
	}
	chat(t, svc, fmt.Sprintf(`{"session_id":%q,"message":"question A2"}`, a))

	raw, _ := json.Marshal(model.requests[2].Contents)
	last := string(raw)
	if strings.Contains(last, "secret from A") {
		t.Errorf("an evicted conversation kept its history: %s", last)
	}
	if !strings.Contains(last, "question A2") {
		t.Errorf("the new question is missing from the request")
	}
}

func TestHandleChatModelWithoutAnAnswer(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{{}}} // a turn that yields nothing
	svc := testService(t, model, fixtureReport())
	events := chat(t, svc, `{"message":"hi"}`)
	last := events[len(events)-1]
	if last.Type != "error" || !strings.Contains(last.Message, "no answer") {
		t.Errorf("last event = %+v, want an error saying the model produced no answer", last)
	}
}

func TestHandleChatModelFactoryFailure(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	svc.modelFactory = func(context.Context) (adkmodel.LLM, error) { return nil, fmt.Errorf("no credentials") }
	events := chat(t, svc, `{"message":"hi"}`)
	last := events[len(events)-1]
	if last.Type != "error" || !strings.Contains(last.Message, "no credentials") {
		t.Errorf("last event = %+v, want the factory error", last)
	}
	// The failure must not leave the conversation (or a slot) held.
	if len(svc.slots) != 0 {
		t.Errorf("%d slots still held after a failed turn", len(svc.slots))
	}
}

func TestHandleChatTimeout(t *testing.T) {
	gated := newGatedModel() // never opened: the turn hangs until the deadline
	svc := gatedService(t, gated)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"hi"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	svc.HandleChat(rec, req)

	// The request context expiring is a client gone, not the chat timeout:
	// no done event may claim a complete answer.
	if strings.Contains(rec.Body.String(), `"type":"done"`) {
		t.Errorf("a cancelled turn reported done: %s", rec.Body.String())
	}
	if len(svc.slots) != 0 {
		t.Errorf("a cancelled turn kept its slot")
	}
}
