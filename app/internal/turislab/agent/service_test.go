package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// scriptedModel replays a scripted conversation: every GenerateContent call
// consumes the next turn, and a turn is a STREAM of responses (partial
// chunks followed by the final one), like a real streaming provider. It
// records every request, so tests can assert the conversation history the
// model saw. It replaces the real provider so tests run offline.
type scriptedModel struct {
	mu       sync.Mutex
	turns    [][]*adkmodel.LLMResponse
	requests []*adkmodel.LLMRequest
}

func (m *scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, stream bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		m.mu.Lock()
		m.requests = append(m.requests, req)
		if len(m.turns) == 0 {
			m.mu.Unlock()
			yield(nil, fmt.Errorf("scripted model exhausted"))
			return
		}
		turn := m.turns[0]
		m.turns = m.turns[1:]
		m.mu.Unlock()
		for _, response := range turn {
			if !yield(response, nil) {
				return
			}
		}
	}
}

func textResponse(text string, partial bool) *adkmodel.LLMResponse {
	return &adkmodel.LLMResponse{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}},
		Partial: partial,
	}
}

// requestCount is how many times the model was called: tests assert on it
// to pin the shape of the tool loop.
func (m *scriptedModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func toolCallResponse(name string, args map[string]any) *adkmodel.LLMResponse {
	return &adkmodel.LLMResponse{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: name, Args: args}},
		}},
	}
}

// testService builds a service with a scripted model factory and, when
// given, an attached report. External MCP servers stay off: their behaviour
// has its own tests.
func testService(t *testing.T, model *scriptedModel, report *model.Report) *Service {
	t.Helper()
	cfg := Config{
		MaxTurns: 5,
		Model:    ModelConfig{Provider: "openai", Name: "scripted"},
	}
	svc, err := New(cfg, logx.New(io.Discard), WithModelFactory(
		func(context.Context) (adkmodel.LLM, error) { return model, nil }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if report != nil {
		svc.SetReport(report)
	}
	return svc
}

// chat drives the chat handler with a JSON body and returns the parsed SSE
// events.
func chat(t *testing.T, svc *Service, body string) []sseEvent {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(body))
	svc.HandleChat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: status %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("chat content type = %q, want text/event-stream", ct)
	}
	return parseSSE(t, rec.Body.String())
}

// parseSSE decodes the data: lines of an event stream.
func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event sseEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("bad sse data %q: %v", line, err)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatalf("no sse events in:\n%s", body)
	}
	return events
}

func TestHandleChatRunsTheToolLoop(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("list_establishments", map[string]any{"tier": "P0"})},
		{textResponse("The queue head is Hotel Sol y Playa.", false)},
	}}
	svc := testService(t, model, fixtureReport())

	events := chat(t, svc, `{"message":"who needs a visit first?"}`)

	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	want := []string{"session", "tool_call", "tool_result", "delta", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event types = %v, want %v", types, want)
	}

	if events[1].Name != "list_establishments" {
		t.Errorf("tool call name = %q, want list_establishments", events[1].Name)
	}
	if !strings.Contains(events[2].Result, "Hotel Sol y Playa") {
		t.Errorf("tool result preview %q does not carry the queue", events[2].Result)
	}
	if events[3].Text != "The queue head is Hotel Sol y Playa." {
		t.Errorf("delta = %q", events[3].Text)
	}
	if events[4].SessionID == "" {
		t.Errorf("done event does not carry the session id")
	}

	// The model must have seen the tool result before producing the answer:
	// the second request carries the function response in its history.
	if model.requestCount() != 2 {
		t.Fatalf("model called %d times, want 2", model.requestCount())
	}
	history, err := json.Marshal(model.requests[1].Contents)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	for _, want := range []string{"who needs a visit first?", "list_establishments", "Hotel Sol y Playa"} {
		if !strings.Contains(string(history), want) {
			t.Errorf("model history does not contain %q", want)
		}
	}
}

func TestHandleChatBoundsAnOversizedToolResult(t *testing.T) {
	huge := strings.Repeat("x", 2*maxToolHistoryChars)
	report := fixtureReport()
	// get_establishment_detail carries the registry fields whole, so a huge
	// name makes the result oversized.
	report.Queue[0].Establishment.Name = huge
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("get_establishment_detail", map[string]any{"id": "est-1"})},
		{textResponse("the establishment is bounded", false)},
	}}
	svc := testService(t, model, report)

	events := chat(t, svc, `{"message":"tell me about est-1"}`)
	if model.requestCount() != 2 {
		t.Fatalf("model called %d times, want 2", model.requestCount())
	}
	history, err := json.Marshal(model.requests[1].Contents)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if len(history) > 2*maxToolHistoryChars {
		t.Errorf("model history is %dB, want it bounded (~%dB)", len(history), 2*maxToolHistoryChars)
	}
	if !strings.Contains(string(history), `"truncated":true`) {
		t.Errorf("model history does not carry the truncation marker")
	}
	if strings.Contains(string(history), huge) {
		t.Errorf("model history carries the full oversized result")
	}
	// The SSE preview keeps carrying the bounded shape of the result.
	var found bool
	for _, event := range events {
		if event.Type == "tool_result" && strings.HasPrefix(event.Result, `{"preview":"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("no tool_result event carries the bounded result shape")
	}
}

func TestHandleChatContinuesASession(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{textResponse("first answer", false)},
		{textResponse("second answer", false)},
	}}
	svc := testService(t, model, fixtureReport())

	events := chat(t, svc, `{"message":"first question"}`)
	sessionID := events[0].SessionID
	if sessionID == "" {
		t.Fatalf("no session id in the first chat")
	}

	events = chat(t, svc, fmt.Sprintf(`{"session_id":%q,"message":"second question"}`, sessionID))
	if events[0].SessionID != sessionID {
		t.Errorf("second chat session id = %q, want %q", events[0].SessionID, sessionID)
	}
	if model.requestCount() != 2 {
		t.Fatalf("model called %d times, want 2", model.requestCount())
	}
}

func TestHandleChatStreamsPartialDeltas(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{
			textResponse("Hel", true),
			textResponse("lo wo", true),
			textResponse("Hello world", false),
		},
	}}
	svc := testService(t, model, fixtureReport())

	var text string
	for _, event := range chat(t, svc, `{"message":"hi"}`) {
		if event.Type == "delta" {
			text += event.Text
		}
	}
	if text != "Hello world" {
		t.Errorf("streamed text = %q, want %q", text, "Hello world")
	}
}

// The last round the budget allows has its tools withdrawn, and a model that
// calls one anyway gets the call stripped: the turn ends with an answer and
// a "done", never with the work lost to an error.
func TestHandleChatEnforcesMaxTurns(t *testing.T) {
	tc := toolCallResponse("list_establishments", map[string]any{})
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{{tc}, {tc}}}
	svc := testService(t, model, fixtureReport())
	svc.cfg.MaxTurns = 2

	events := chat(t, svc, `{"message":"loop"}`)
	var sawToolCall, sawDone bool
	text := ""
	for _, event := range events {
		switch event.Type {
		case "tool_call":
			sawToolCall = true
		case "delta":
			text += event.Text
		case "done":
			sawDone = true
		case "error":
			t.Fatalf("the loop ended with an error: %q", event.Message)
		}
	}
	if !sawToolCall {
		t.Errorf("the first round never called a tool")
	}
	if !sawDone {
		t.Errorf("the chat did not finish with done")
	}
	if text != budgetExhaustedText {
		t.Errorf("answer = %q, want the budget-exhausted text", text)
	}
	// The final request must have had its tools withdrawn and carry the
	// final-round instruction.
	last := model.requests[len(model.requests)-1]
	if last.Config == nil || last.Config.ToolConfig == nil {
		t.Fatalf("the last request has no ToolConfig: %+v", last.Config)
	}
	mode := last.Config.ToolConfig.FunctionCallingConfig.Mode
	if mode != genai.FunctionCallingConfigModeNone {
		t.Errorf("last request tool mode = %v, want NONE", mode)
	}
	if !strings.Contains(last.Config.SystemInstruction.Parts[len(last.Config.SystemInstruction.Parts)-1].Text, "last step") {
		t.Errorf("the last request does not carry the final-round instruction: %+v", last.Config.SystemInstruction)
	}
}

// answerOnly strips the tool calls of a last-round response: there is no
// round left to run them. A response left without text gets one saying the
// budget ran out.
func TestAnswerOnly(t *testing.T) {
	mixed := &adkmodel.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{
		{Text: "The queue head needs a visit."},
		{FunctionCall: &genai.FunctionCall{Name: "list_establishments"}},
	}}}
	got := answerOnly(mixed)
	if len(got.Content.Parts) != 1 {
		t.Fatalf("answerOnly kept %d parts, want 1 (the text)", len(got.Content.Parts))
	}
	if got.Content.Parts[0].Text != "The queue head needs a visit." {
		t.Errorf("text part = %q", got.Content.Parts[0].Text)
	}
	if len(mixed.Content.Parts) != 2 {
		t.Errorf("answerOnly mutated its input")
	}

	callOnly := answerOnly(&adkmodel.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{Name: "list_establishments"}},
	}}})
	if len(callOnly.Content.Parts) != 1 || callOnly.Content.Parts[0].Text != budgetExhaustedText {
		t.Errorf("a call-only response must be replaced by the budget text, got %+v", callOnly.Content.Parts)
	}

	if answerOnly(nil) != nil {
		t.Errorf("answerOnly(nil) must stay nil")
	}
}

func TestHandleChatGuards(t *testing.T) {
	model := &scriptedModel{}
	svc := testService(t, model, nil) // no report attached yet

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"hi"}`))
	svc.HandleChat(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("chat before the scan: status %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "scanning") {
		t.Errorf("503 body %q does not say scanning", rec.Body.String())
	}

	svc.SetReport(fixtureReport())

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"   "}`))
	svc.HandleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty message: status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`not json`))
	svc.HandleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body: status %d, want 400", rec.Code)
	}
	if model.requestCount() != 0 {
		t.Errorf("the model was called %d times during guard rejections", model.requestCount())
	}
}

func TestInfoPayload(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	info := svc.Info()
	if !info.Enabled {
		t.Errorf("info.Enabled = false")
	}
	if info.Model != "scripted" {
		t.Errorf("info.Model = %q", info.Model)
	}
	if len(info.Suggestions) == 0 {
		t.Errorf("info.Suggestions is empty")
	}
}

// A chat whose client goes away mid-run must return: the request context is
// cancelled and the run unwinds.
func TestHandleChatClientDisconnect(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("list_establishments", map[string]any{})},
		{textResponse("done", false)},
	}}
	svc := testService(t, model, fixtureReport())

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"hi"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	cancel() // the client goes away before the run starts
	svc.HandleChat(rec, req)
}

func TestHandleChatRejectsAnInvalidSessionID(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	rec := post(svc, `{"session_id":"../etc/passwd","message":"hi"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid session_id") {
		t.Errorf("bad session id: %d %s, want 400 invalid session_id", rec.Code, rec.Body.String())
	}
}

// A round that writes text and calls a tool streams the text first, the
// order the model produced it in: sent after the calls, it would land
// between the calls and their results and glue to the next round's text.
func TestHandleChatStreamsRoundTextBeforeItsToolCalls(t *testing.T) {
	mixed := &adkmodel.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{
		{Text: "Checking the queue first."},
		{FunctionCall: &genai.FunctionCall{Name: "list_establishments", Args: map[string]any{}}},
	}}}
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{{mixed}, {textResponse("The top pick is est-1.", false)}}}
	svc := testService(t, model, fixtureReport())

	var order []string
	for _, event := range chat(t, svc, `{"message":"queue?"}`) {
		switch event.Type {
		case "delta":
			order = append(order, "delta:"+event.Text)
		case "tool_call":
			order = append(order, "tool_call")
		}
	}
	joined := strings.Join(order, ",")
	want := "delta:Checking the queue first.,tool_call,delta:The top pick is est-1."
	if joined != want {
		t.Fatalf("event order = %q, want %q", joined, want)
	}
}

// instruction of a narrow question must not promise more rounds: the agent
// ends the answer there, it does not offer to sweep the whole queue.
func TestInstructionEndsNarrowAnswersWithoutAnOffer(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	if !strings.Contains(svc.instruction(), "do NOT offer to continue") {
		t.Errorf("the prompt does not forbid the follow-up offer on a narrow question")
	}
}

func TestInstructionCarriesTheScanDigest(t *testing.T) {
	svc := testService(t, &scriptedModel{}, fixtureReport())
	prompt := svc.instruction()
	for _, want := range []string{"3 establishments registered", "P0 1", "92%", "confirmed closed"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt digest does not contain %q:\n%s", want, prompt)
		}
	}
}

// Without an injected model factory, the service must refuse to start when
// no API key can be resolved: a missing credential should stop the startup,
// not the first chat.
func TestNewFailsFastWithoutModelKey(t *testing.T) {
	t.Setenv("DOCTOR_MISSING_KEY", "")
	cfg := Config{
		MaxTurns: 5,
		Model:    ModelConfig{Provider: "openai", Name: "scripted", APIKeyEnv: "DOCTOR_MISSING_KEY"},
	}
	if _, err := New(cfg, logx.New(io.Discard)); err == nil {
		t.Errorf("New succeeded without any model key, want an error")
	}
}
