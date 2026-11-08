package agent

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/datadope/turislab-doctor/internal/logx"
	adkmodel "google.golang.org/adk/v2/model"
)

// lockedBuffer is a bytes.Buffer safe for the concurrent writes of a chat.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// loggedService is testService with its log captured.
func loggedService(t *testing.T, model *scriptedModel) (*Service, *lockedBuffer) {
	t.Helper()
	svc := testService(t, model, fixtureReport())
	out := &lockedBuffer{}
	svc.log = logx.New(out)
	return svc, out
}

// agentLines returns the agent-section lines of a captured log.
func agentLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if strings.Contains(line, " agent ") {
			lines = append(lines, line)
		}
	}
	return lines
}

const loggedSession = "abcdef0123456789abcdef0123456789"

func TestHandleChatLogsAFailedToolCall(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("get_establishment_detail", map[string]any{"id": "nope"})},
		{textResponse("that establishment is not in the queue", false)},
	}}
	svc, out := loggedService(t, model)

	chat(t, svc, `{"message":"detail please","session_id":"`+loggedSession+`"}`)

	log := out.String()
	want := "[abcdef01] tool ✗ get_establishment_detail: "
	if !strings.Contains(log, want) {
		t.Fatalf("log has no %q line:\n%s", want, log)
	}
	if !strings.Contains(log, `no establishment with id "nope"`) {
		t.Errorf("the failure line does not carry the tool's error:\n%s", log)
	}
	for _, line := range agentLines(log) {
		if strings.Contains(line, "tool ✗") && !strings.Contains(line, "WARN") {
			t.Errorf("a failed tool call is not a warning: %q", line)
		}
	}
}

func TestHandleChatDoesNotLogSuccessfulToolsAsFailures(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("get_report_summary", map[string]any{})},
		{textResponse("all good", false)},
	}}
	svc, out := loggedService(t, model)

	chat(t, svc, `{"message":"summary","session_id":"`+loggedSession+`"}`)

	if log := out.String(); strings.Contains(log, "tool ✗") {
		t.Errorf("a successful tool call was logged as failed:\n%s", log)
	}
}

func TestHandleChatTagsEveryAgentLineWithTheSession(t *testing.T) {
	model := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("list_establishments", map[string]any{"tier": "P0"})},
		{toolCallResponse("get_establishment_detail", map[string]any{"id": "nope"})},
		{textResponse("done", false)},
	}}
	svc, out := loggedService(t, model)

	chat(t, svc, `{"message":"go","session_id":"`+loggedSession+`"}`)

	lines := agentLines(out.String())
	if len(lines) < 5 {
		t.Fatalf("expected the chat, tool, round and failure lines, got:\n%s", strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if !strings.Contains(line, "[abcdef01]") {
			t.Errorf("agent line without the session: %q", line)
		}
	}
}

func TestToolFailure(t *testing.T) {
	long := strings.Repeat("é", 400)
	for _, tc := range []struct {
		name   string
		result map[string]any
		err    error
		want   string
	}{
		{"success", map[string]any{"ok": true}, nil, ""},
		{"nil result", nil, nil, ""},
		{"go error", nil, errors.New("boom"), "boom"},
		{"error field", map[string]any{"error": "datasource unreachable"}, nil, "datasource unreachable"},
		{"empty error field", map[string]any{"error": ""}, nil, ""},
		{"structured error", map[string]any{"error": map[string]any{"code": 502}}, nil, `{"code":502}`},
		{"go error wins", map[string]any{"error": "inner"}, errors.New("outer"), "outer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolFailure(tc.result, tc.err); got != tc.want {
				t.Errorf("toolFailure = %q, want %q", got, tc.want)
			}
		})
	}

	got := toolFailure(nil, errors.New(long))
	if len(got) > 300+len("…") || !strings.HasSuffix(got, "…") {
		t.Errorf("a long error is not bounded: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "é") || strings.ContainsRune(got, '�') {
		t.Errorf("the bound split a UTF-8 character")
	}
}
