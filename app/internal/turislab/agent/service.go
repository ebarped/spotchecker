// Package agent embeds the inspection assistant: an ADK Go agent that answers
// over the current scan report through local tools (list_establishments, ...)
// and, optionally, external MCP servers by URL.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
	"google.golang.org/genai"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

const (
	appName = "turislab-doctor"
	userID  = "doctor"

	// maxToolResultChars bounds what a tool result copies into the SSE
	// stream. The model consumes the full result; the UI only needs a
	// useful preview.
	maxToolResultChars = 2000

	// maxToolHistoryChars bounds what a tool returns to the model. The
	// result is what the model consumes AND what the session history
	// stores — an unbounded one (a base64 render, a huge JSON dump)
	// inflates every later request of the chat until the provider starts
	// answering 500s. Bounded at the source, the history stays small no
	// matter how long the conversation runs.
	maxToolHistoryChars = 8000

	// chatTimeout bounds a whole chat request, tool calls included. Long
	// enough for multi-tool turns, short enough that a hung tool call
	// surfaces as an error event instead of a stuck browser tab. The
	// server's route group allows slightly more so the handler can still
	// send the timeout error event to the client.
	ChatTimeout = 10 * time.Minute

	// maxMessageChars bounds a user message. A longer one is refused
	// rather than cut: silently dropping part of a question changes it.
	maxMessageChars = 8000

	// maxRequestBytes bounds the chat request body; a larger one is
	// answered 413 instead of being read.
	maxRequestBytes = 64 << 10

	// maxConcurrentChats bounds the turns running at once, across
	// conversations: each one holds an MCP session and bills model rounds.
	maxConcurrentChats = 8
)

// finalRoundInstruction is appended to the system instruction of the last
// model round the budget allows: the model must answer with what it has.
const finalRoundInstruction = "\n\nThis is your last step for this message: do not call any tool. Answer now with what you have found, say what is still unverified, and offer to continue."

// budgetExhaustedText replaces a last-round answer that still tried to call
// tools and wrote nothing.
const budgetExhaustedText = "I used up this message's step budget before reaching a conclusion. Ask me to continue and I will pick up from here."

// defaultSuggestions are the suggested prompts of the UI.
var defaultSuggestions = []string{
	"Summarize this scan for the inspection team: which establishments need a visit first and why",
	"Which establishments are closed or at highest closure risk, and what evidence supports it",
	"Which municipalities concentrate most of the risk, and where should we send inspectors",
	"Pick the top establishment of the queue and go through its evidence in detail",
}

// Service runs the agent for the HTTP server.
type Service struct {
	cfg         Config
	log         *logx.Logger
	report      atomicReport
	sessions    session.Service
	registry    *sessionRegistry
	slots       chan struct{}
	suggestions []string
	// modelFactory builds the LLM for each chat request; injectable so
	// tests can drive the loop with a scripted model.
	modelFactory func(context.Context) (adkmodel.LLM, error)
}

// Option customizes the Service.
type Option func(*Service)

// WithModelFactory replaces the configured LLM construction. Tests use it
// to run the whole agent loop without touching a real provider.
func WithModelFactory(factory func(context.Context) (adkmodel.LLM, error)) Option {
	return func(s *Service) { s.modelFactory = factory }
}

// WithReport attaches a report at construction time (tests, CLI use).
func WithReport(report *model.Report) Option {
	return func(s *Service) { s.report.set(report) }
}

// New builds the agent service from its configuration. The report is
// attached later with SetReport, once the scanner has produced one.
func New(cfg Config, log *logx.Logger, opts ...Option) (*Service, error) {
	s := &Service{
		cfg:         cfg,
		log:         log,
		sessions:    session.InMemoryService(),
		registry:    newSessionRegistry(sessionIdleTTL, maxSessions),
		slots:       make(chan struct{}, maxConcurrentChats),
		suggestions: cfg.Suggestions,
	}
	if len(s.suggestions) == 0 {
		s.suggestions = defaultSuggestions
	}
	s.registry.onEvict = s.dropHistory
	for _, opt := range opts {
		opt(s)
	}
	if s.modelFactory == nil {
		// Fail fast when the default factory would have no key to use: a
		// missing credential should stop the startup, not the first chat.
		if ResolveAPIKey(cfg.Model) == "" {
			return nil, fmt.Errorf(
				"agent model: set agent.model.api_key or the environment variable %s (agent.model.api_key_env)",
				cfg.Model.APIKeyEnv)
		}
		factory, err := ModelFactory(cfg.Model)
		if err != nil {
			return nil, err
		}
		s.modelFactory = factory
	}
	return s, nil
}

// ResolveAPIKey returns the model API key: the literal agent.model.api_key
// when set, otherwise the value of the environment variable named by
// agent.model.api_key_env. Empty means no key could be resolved.
func ResolveAPIKey(am ModelConfig) string {
	if am.APIKey != "" {
		return am.APIKey
	}
	return os.Getenv(am.APIKeyEnv)
}

// ModelFactory builds the configured provider client per request. Exported
// for tooling that needs the same client construction as the agent (the
// llmprobe command).
func ModelFactory(am ModelConfig) (func(context.Context) (adkmodel.LLM, error), error) {
	return func(ctx context.Context) (adkmodel.LLM, error) {
		apiKey := ResolveAPIKey(am)
		if apiKey == "" {
			return nil, fmt.Errorf("no model API key: set agent.model.api_key or the environment variable %s (agent.model.api_key_env)", am.APIKeyEnv)
		}
		switch am.Provider {
		case "gemini":
			return gemini.NewModel(ctx, am.Name, &genai.ClientConfig{APIKey: apiKey})
		case "openai":
			cc := &openaimodel.ClientConfig{APIKey: apiKey, BaseURL: am.BaseURL}
			if am.BaseURL != "" {
				// OpenAI-compatible endpoints (vLLM, LiteLLM, Azure
				// gateways...) overwhelmingly serve the chat completions
				// API, not the newer /v1/responses one.
				cc.API = openaimodel.APIChatCompletions
			}
			return openaimodel.NewModel(ctx, am.Name, cc)
		default:
			return nil, fmt.Errorf("unknown agent model provider %q", am.Provider)
		}
	}, nil
}

// SetReport attaches the current scan report; the agent tools answer from
// it from this moment on.
func (s *Service) SetReport(report *model.Report) {
	s.report.set(report)
}

// Info is the payload of GET /api/agent/info.
type Info struct {
	Enabled     bool     `json:"enabled"`
	Model       string   `json:"model"`
	Suggestions []string `json:"suggestions"`
}

// Info describes the agent for the UI.
func (s *Service) Info() Info {
	return Info{
		Enabled:     true,
		Model:       s.cfg.Model.Name,
		Suggestions: s.suggestions,
	}
}

// HandleInfo serves GET /api/agent/info.
func (s *Service) HandleInfo(w http.ResponseWriter, r *http.Request) {
	writeAgentJSON(w, http.StatusOK, s.Info())
}

// HandleChat serves POST /api/agent/chat with a text/event-stream body.
func (s *Service) HandleChat(w http.ResponseWriter, r *http.Request) {
	if s.report.report() == nil {
		writeAgentJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "scanning"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAgentJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body is too large"})
			return
		}
		writeAgentJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body"})
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeAgentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeAgentJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if utf8.RuneCountInString(req.Message) > maxMessageChars {
		writeAgentJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("message is longer than %d characters", maxMessageChars)})
		return
	}
	if req.SessionID != "" && !sessionIDPattern.MatchString(req.SessionID) {
		writeAgentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session_id"})
		return
	}
	if req.SessionID == "" {
		if req.SessionID, err = newSessionID(); err != nil {
			writeAgentJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create a session"})
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAgentJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeAgentJSON(w, http.StatusTooManyRequests, map[string]string{"error": "the assistant is busy with other chats, try again in a moment"})
		return
	}

	release, known, evicted, err := s.registry.acquire(req.SessionID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, errSessionTooLong) {
			status = http.StatusUnprocessableEntity
		}
		writeAgentJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	defer release()
	if len(evicted) > 0 {
		s.log.Section("agent").Info("dropped %d idle conversations (%d held)", len(evicted), s.registry.size())
	}
	if req.SessionID != "" && !known {
		// Expired, evicted or from before a restart: the runner starts it
		// afresh, without the earlier turns.
		s.log.Section("agent").Info("[%s] session unknown to this server, starting it fresh", shortID(req.SessionID))
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // reverse proxies must not buffer the stream
	w.WriteHeader(http.StatusOK)

	s.emit(w, flusher, sseEvent{Type: "session", SessionID: req.SessionID})
	s.log.Section("agent").Info("[%s] chat ← session %s: %q", shortID(req.SessionID), req.SessionID, preview(req.Message))

	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), ChatTimeout)
	defer cancel()
	err = s.run(ctx, w, flusher, req.SessionID, req.Message)
	switch {
	case err == nil:
		s.emit(w, flusher, sseEvent{Type: "done", SessionID: req.SessionID})
		s.log.Section("agent").Info("[%s] chat → done in %s", shortID(req.SessionID), time.Since(started).Round(time.Millisecond))
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.emit(w, flusher, sseEvent{Type: "error", Message: "the chat timed out"})
		s.log.Section("agent").Warn("[%s] chat ✗ timed out after %s", shortID(req.SessionID), time.Since(started).Round(time.Second))
	case ctx.Err() != nil:
		// The client went away (closed tab, new chat): nobody to tell.
		s.log.Section("agent").Info("[%s] chat ✗ client disconnected after %s", shortID(req.SessionID), time.Since(started).Round(time.Second))
	default:
		s.emit(w, flusher, sseEvent{Type: "error", Message: err.Error()})
		s.log.Section("agent").Warn("[%s] chat ✗ %v", shortID(req.SessionID), err)
	}
}

// dropHistory deletes the history of a conversation the registry evicted.
func (s *Service) dropHistory(id string) {
	// Not found is fine: a session the runner never created.
	_ = s.sessions.Delete(context.Background(), &session.DeleteRequest{AppName: appName, UserID: userID, SessionID: id})
}

// preview shortens a message for the log line.
func preview(message string) string {
	message = strings.ReplaceAll(message, "\n", " ")
	return truncate(message, 80)
}

// run drives one agent turn and streams its events.
func (s *Service) run(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sessionID, message string) error {
	llm, err := s.modelFactory(ctx)
	if err != nil {
		return err
	}

	toolsets, closers, err := s.buildToolsets(ctx)
	if err != nil {
		return err
	}
	defer func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}()

	// rounds counts completed model rounds (streaming chunks are not
	// rounds). The last round the budget allows has its tools withdrawn so
	// the model writes an answer instead of the turn dying mid-loop.
	rounds, maxRounds := 0, max(s.cfg.MaxTurns, 1)
	cfg := llmagent.Config{
		Name:        "doctor",
		Model:       llm,
		Description: "Inspection assistant for the turislab-doctor scan",
		Instruction: s.instruction(),
		Tools:       buildTools(&s.report),
		// Every tool result — local or MCP — goes through here before the
		// model consumes it and the session history stores it. Bounded at
		// the source, the history stays small no matter how long the
		// conversation runs: an unbounded result (a base64 render, a huge
		// JSON dump) would inflate every later request until the provider
		// starts answering 500s.
		AfterToolCallbacks: []llmagent.AfterToolCallback{
			func(_ agent.Context, t tool.Tool, _ map[string]any, result map[string]any, err error) (map[string]any, error) {
				if msg := toolFailure(result, err); msg != "" {
					s.log.Section("agent").Warn("[%s] tool ✗ %s: %s", shortID(sessionID), t.Name(), msg)
				}
				if err == nil {
					boundResult(result, maxToolHistoryChars)
				}
				return result, err
			},
		},
		Toolsets: toolsets,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			func(_ agent.Context, req *adkmodel.LLMRequest) (*adkmodel.LLMResponse, error) {
				if rounds+1 >= maxRounds {
					s.log.Section("agent").Info("[%s] model round %d is the last of the budget: no more tools", shortID(sessionID), rounds+1)
					withdrawTools(req)
				}
				return nil, nil
			},
		},
		AfterModelCallbacks: []llmagent.AfterModelCallback{
			func(_ agent.Context, response *adkmodel.LLMResponse, responseErr error) (*adkmodel.LLMResponse, error) {
				if responseErr == nil && response != nil && response.Partial {
					return response, nil // a streaming chunk, not a round
				}
				rounds++
				if responseErr != nil {
					s.log.Section("agent").Warn("[%s] model round %d error: %v", shortID(sessionID), rounds, responseErr)
					return nil, responseErr
				}
				if response != nil {
					textLen, calls := 0, 0
					if response.Content != nil {
						for _, part := range response.Content.Parts {
							if part.Text != "" {
								textLen += len(part.Text)
							}
							if part.FunctionCall != nil {
								calls++
							}
						}
					}
					s.log.Section("agent").Info(
						"[%s] model round %d — %dB text, %d function calls, partial %v, finish %s",
						shortID(sessionID), rounds, textLen, calls, response.Partial, response.FinishReason)
				}
				if rounds > maxRounds {
					// Unreachable while the last round drops its calls; kept
					// as the hard stop against a runaway loop.
					return nil, fmt.Errorf("the agent exceeded its %d model rounds budget", maxRounds)
				}
				if rounds == maxRounds {
					response = answerOnly(response)
				}
				return response, nil
			},
		},
	}
	rootAgent, err := llmagent.New(cfg)
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	r, err := runner.New(runner.Config{
		AppName:           appName,
		Agent:             rootAgent,
		SessionService:    s.sessions,
		AutoCreateSession: true,
	})
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	sentText := ""
	msg := &genai.Content{Role: "user", Parts: []*genai.Part{{Text: message}}}
	emitted, pending := 0, false
	for event, err := range r.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			if ctx.Err() != nil {
				// Non-nil so HandleChat takes its timeout branch: returning
				// nil here sent "done" and the UI showed a cut-off answer as
				// if it were complete.
				return ctx.Err()
			}
			return fmt.Errorf("agent run: %w", err)
		}
		if event.Author == "user" {
			continue
		}
		var calls, texts int
		sentText, calls, texts = s.streamContent(w, flusher, event, sessionID, sentText)
		emitted += calls + texts
		// A tool call opens an unfinished step; only text closes it. The
		// runner's range can end right after a tool call when the next
		// model round dies upstream without a single chunk — without this
		// the chat "finishes" mid-loop with no answer.
		if calls > 0 {
			pending = true
		}
		if texts > 0 {
			pending = false
		}
	}
	if emitted == 0 || pending {
		// The model never answered: without this, the chat "finishes"
		// silently and the user stares at an empty bubble.
		return fmt.Errorf("the model produced no answer")
	}
	return nil
}

// streamContent maps one agent event to SSE events. Partial events stream
// their text as it arrives; the final event of a round carries the round's
// whole text, of which only what the partials did not cover is sent — then
// the round is closed, so the next round starts from scratch. Function calls
// and responses become tool events. It reports how many tool calls and text
// deltas it emitted, so a run that ends without closing its last tool step
// can be detected as the model failure it is.
func (s *Service) streamContent(w http.ResponseWriter, flusher http.Flusher, event *session.Event, sessionID, sentText string) (string, int, int) {
	if event.Content == nil {
		return sentText, 0, 0
	}
	calls, texts := 0, 0
	if !event.Partial {
		// The round's text goes out before its tool calls, the order the
		// model produced it in: sent after them, it would land between the
		// calls and their results and glue to the next round's text.
		var full strings.Builder
		for _, part := range event.Content.Parts {
			if part.Text != "" && !part.Thought {
				full.WriteString(part.Text)
			}
		}
		if remaining := strings.TrimPrefix(full.String(), sentText); remaining != "" {
			s.emit(w, flusher, sseEvent{Type: "delta", Text: remaining})
			texts++
		}
		sentText = ""
	}
	for _, part := range event.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			s.log.Section("agent").Info("[%s] tool → %s %s", shortID(sessionID), part.FunctionCall.Name, compactArgs(args))
			s.emit(w, flusher, sseEvent{Type: "tool_call", ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Args: args})
			calls++
		case part.FunctionResponse != nil:
			s.emit(w, flusher, sseEvent{Type: "tool_result", ID: part.FunctionResponse.ID, Name: part.FunctionResponse.Name, Result: resultPreview(part.FunctionResponse.Response)})
		case event.Partial && part.Text != "" && !part.Thought:
			sentText += part.Text
			s.emit(w, flusher, sseEvent{Type: "delta", Text: part.Text})
			texts++
		}
	}
	return sentText, calls, texts
}

// withdrawTools turns a model request into a final-answer one: tool use
// switched off and the instruction told to answer now. The declarations
// stay — some providers reject a history holding tool calls when the
// request declares no tools — and answerOnly catches a model that calls
// one anyway.
func withdrawTools(req *adkmodel.LLMRequest) {
	cfg := genai.GenerateContentConfig{}
	if req.Config != nil {
		cfg = *req.Config
	}
	cfg.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
		Mode: genai.FunctionCallingConfigModeNone,
	}}
	instruction := &genai.Content{Role: "system"}
	if cfg.SystemInstruction != nil {
		instruction.Role = cfg.SystemInstruction.Role
		instruction.Parts = append(instruction.Parts, cfg.SystemInstruction.Parts...)
	}
	instruction.Parts = append(instruction.Parts, &genai.Part{Text: finalRoundInstruction})
	cfg.SystemInstruction = instruction
	req.Config = &cfg
}

// answerOnly strips the tool calls of a last-round response: there is no
// round left to run them. A response left with no text gets one saying the
// budget ran out, so the turn ends with an answer instead of nothing.
func answerOnly(response *adkmodel.LLMResponse) *adkmodel.LLMResponse {
	if response == nil || response.Content == nil {
		return response
	}
	out := *response
	content := *response.Content
	content.Parts = nil
	hasText := false
	for _, part := range response.Content.Parts {
		if part.FunctionCall != nil {
			continue
		}
		if part.Text != "" && !part.Thought {
			hasText = true
		}
		content.Parts = append(content.Parts, part)
	}
	if !hasText {
		content.Parts = append(content.Parts, &genai.Part{Text: budgetExhaustedText})
	}
	out.Content = &content
	return &out
}

// toolFailure describes why a tool call failed, or "" when it did not. A
// local tool fails with a Go error; an MCP tool usually answers normally
// with the failure inside the result ({"error": ...}). Either way the model
// sees it — this is what makes it visible in the server log too.
func toolFailure(result map[string]any, err error) string {
	if err != nil {
		return truncate(err.Error(), 300)
	}
	switch v := result["error"].(type) {
	case nil:
		return ""
	case string:
		if v == "" {
			return ""
		}
		return truncate(v, 300)
	default:
		raw, jerr := json.Marshal(v)
		if jerr != nil {
			return "unprintable error"
		}
		return truncate(string(raw), 300)
	}
}

// shortID is the session id as the logs show it: enough to tell the chats
// apart and to grep for the full id the chat ← line prints.
func shortID(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

// compactArgs renders tool arguments on one bounded log line.
func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return truncate(string(raw), 160)
}

// resultPreview flattens a tool response into a bounded string for the UI.
func resultPreview(response any) string {
	out, err := json.Marshal(response)
	if err != nil {
		return ""
	}
	return truncate(string(out), maxToolResultChars)
}

// buildToolsets collects the MCP toolsets of this chat: every configured
// external server, by URL. They connect lazily and must be closed when the
// chat ends.
func (s *Service) buildToolsets(ctx context.Context) ([]tool.Toolset, []io.Closer, error) {
	var toolsets []tool.Toolset
	var closers []io.Closer

	for _, server := range s.cfg.MCP {
		transport := &mcpgo.StreamableClientTransport{
			Endpoint:   server.URL,
			HTTPClient: &http.Client{Transport: newHeaderTransport(server.Headers)},
		}
		ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
		if err != nil {
			return nil, nil, fmt.Errorf("connect to the mcp server %q: %w", server.Name, err)
		}
		toolsets = append(toolsets, ts)
		closers = append(closers, ts.(io.Closer))
	}
	return toolsets, closers, nil
}

// instruction builds the system prompt for one run: the fixed rules plus a
// one-line digest of the current scan.
func (s *Service) instruction() string {
	var b strings.Builder
	b.WriteString(`You are Turislab Doctor, the inspection assistant of turislab-doctor. You help the user work the inspector visit queue built from the latest scan of tourist establishments: who is closed, who is at risk, and who must be visited first.

Rules:
- Ground every statement in tool output. Cite establishments by id, name and municipality.
- The local tools (get_report_summary, list_establishments, get_establishment_detail) describe the current scan report: the priority queue, the score breakdown of one establishment and the summary of the whole scan.
- The evidence of an establishment (get_establishment_detail) is what justifies its place in the queue: read it before asserting why an establishment is closed or at risk.
- Never invent data. If a tool fails, say so and move on. Answer in the language of the user's question.

Scope — one thing at a time:
- Before calling tools, decide the single most important target for this turn. For a broad request ("which establishments need a visit", "what is the state of the scan"), take only the top item — the queue order of list_establishments gives it — and analyze that one well.
- End such an answer by naming what comes next and asking whether to continue, e.g. "Next is <establishment> (<tier>, <N> evidence signals). Shall I go on with it?". Never sweep every establishment in one turn.
- A narrow request (one establishment, one municipality, one tier) gets a direct, complete answer. End it there: do NOT offer to continue with another establishment or item.

Plan before you call — think step by step:
- Before each tool call, reason first: state what you still need to find out, name the tool that answers it, and say with what arguments. Only then call it.
- If a result changes the plan, say what you learned and name the next tool before calling it. Do not call tools blindly or in parallel without a stated reason.

Tool discipline:
- Aim for at most about 6 tool calls per turn; the turn has a hard budget of model rounds and running out of it loses the whole answer.
- Prefer the narrowest tool: get_report_summary to frame, list_establishments filtered by tier/municipality/status, get_establishment_detail for one establishment.
- Never repeat a call with the same arguments. If a result says truncated or has next_offset, narrow the filters or page with offset — do not ask for a bigger limit.
- Once you have enough to answer, stop calling tools and write the answer.`)

	if rep := s.report.report(); rep != nil {
		q := rep.Summary
		fmt.Fprintf(&b, "\n\nCurrent scan: %d establishments registered, %d scored: P0 %d, P1 %d, P2 %d, P3 %d, collect-data %d. %d confirmed closed, %d low confidence, coverage %.0f%%.",
			rep.EstablishmentCount, q.Total, q.P0, q.P1, q.P2, q.P3, q.CollectData,
			q.ClosureConfirmed, q.LowConfidence, q.Coverage)
		switch {
		case rep.ScanRunning:
			b.WriteString(" A scan is running right now: numbers may change.")
		case rep.ScanError != "":
			fmt.Fprintf(&b, " The last scan ended with an error: %s.", rep.ScanError)
		}
	}
	return b.String()
}

// sseEvent is one JSON payload of the chat stream. ID pairs a tool_result
// with its tool_call: the same tool can run several times in one turn and
// results need not arrive in order.
type sseEvent struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Name      string         `json:"name,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Result    string         `json:"result,omitempty"`
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Message   string         `json:"message,omitempty"`
}

func (s *Service) emit(w http.ResponseWriter, flusher http.Flusher, event sseEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

// Close releases the resources the service holds. Nothing lives beyond a
// chat — MCP sessions are closed with their toolset — so this is kept for
// symmetry with the server lifecycle.
func (s *Service) Close() error {
	return nil
}

// headerTransport sets the configured headers on every outgoing request.
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	for k, v := range t.headers {
		out.Header.Set(k, v)
	}
	return t.base.RoundTrip(out)
}

func newHeaderTransport(headers map[string]string) http.RoundTripper {
	return &headerTransport{headers: headers, base: http.DefaultTransport}
}

// newBearerTransport is newHeaderTransport for a single Authorization value.
func newBearerTransport(token string) http.RoundTripper {
	return newHeaderTransport(map[string]string{"Authorization": "Bearer " + token})
}

func newSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// writeAgentJSON writes a small JSON body for the agent's non-stream
// responses (info, errors before the stream starts).
func writeAgentJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
