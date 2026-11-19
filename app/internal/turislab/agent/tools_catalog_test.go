package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datadope/turislab-doctor/internal/logx"
	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"
)

// catalogConfig is the config the catalog tests build services with: the
// model is scripted, the report is the fixture and the MCP servers are the
// ones each test adds.
func catalogConfig(mcp ...MCPServer) Config {
	return Config{
		Model:       ModelConfig{Provider: "openai", Name: "scripted", APIKey: "test-key"},
		MaxTurns:    5,
		Suggestions: []string{"top of the queue"},
		MCP:         mcp,
	}
}

// TestToolsCatalogBuiltin checks the built-in source: the three report
// helpers with their descriptions.
func TestToolsCatalogBuiltin(t *testing.T) {
	svc, err := New(catalogConfig(), logx.New(io.Discard), WithReport(fixtureReport()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := svc.Tools(context.Background())
	if len(catalog.Builtin) != 1 {
		t.Fatalf("got %d builtin sources, want 1 (report)", len(catalog.Builtin))
	}
	if len(catalog.External) != 0 {
		t.Errorf("got %d external sources with no MCP configured, want 0", len(catalog.External))
	}

	report := catalog.Builtin[0]
	if report.Key != "report" || report.Error != "" {
		t.Fatalf("report source = %+v", report)
	}
	if len(report.Tools) != 4 {
		t.Fatalf("report tools = %d, want 4", len(report.Tools))
	}
	names := map[string]bool{}
	for _, tool := range report.Tools {
		if tool.Name == "" || tool.Description == "" {
			t.Errorf("report tool %q carries no name or description", tool.Name)
		}
		names[tool.Name] = true
	}
	for _, want := range []string{"get_report_summary", "list_establishments", "get_establishment_detail"} {
		if !names[want] {
			t.Errorf("the catalog misses the %q tool: %v", want, names)
		}
	}
}

// externalMCPFixture serves a minimal MCP server over streamable HTTP with
// the given tool names, the way an external MCP server does.
func externalMCPFixture(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	if len(names) == 0 {
		names = []string{"echo"}
	}
	server := mcpgo.NewServer(&mcpgo.Implementation{Name: "test-mcp", Version: "0"}, nil)
	for _, name := range names {
		mcpgo.AddTool(server, &mcpgo.Tool{Name: name, Description: "Echoes whatever it is given"},
			func(ctx context.Context, req *mcpgo.CallToolRequest, input struct{}) (*mcpgo.CallToolResult, struct{}, error) {
				return &mcpgo.CallToolResult{Content: []mcpgo.Content{
					&mcpgo.TextContent{Text: "echo"},
				}}, struct{}{}, nil
			})
	}
	return httptest.NewServer(mcpgo.NewStreamableHTTPHandler(
		func(r *http.Request) *mcpgo.Server { return server }, nil))
}

func TestToolsCatalogExternal(t *testing.T) {
	fake := externalMCPFixture(t)
	defer fake.Close()

	svc, err := New(catalogConfig(MCPServer{Name: "test", URL: fake.URL}), logx.New(io.Discard))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := svc.Tools(context.Background())
	if len(catalog.External) != 1 {
		t.Fatalf("got %d external sources, want 1", len(catalog.External))
	}
	source := catalog.External[0]
	if source.Key != "mcp:test" || source.Label != "test" || source.Error != "" {
		t.Fatalf("external source = %+v", source)
	}
	if len(source.Tools) != 1 || source.Tools[0].Name != "echo" ||
		!strings.Contains(source.Tools[0].Description, "Echoes") {
		t.Errorf("external tools = %+v", source.Tools)
	}
	if len(catalog.Builtin) == 0 || catalog.Builtin[0].Key != "report" {
		t.Errorf("builtin sources = %+v", catalog.Builtin)
	}
}

// The sidebar only lists the tools the model is allowed to call.
func TestToolsCatalogRespectsMCPAllowlist(t *testing.T) {
	fake := externalMCPFixture(t, "browser_navigate", "browser_evaluate")
	defer fake.Close()

	cfg := catalogConfig(MCPServer{
		Name:  "browser",
		URL:   fake.URL,
		Tools: []string{"browser_navigate"},
	})
	svc, err := New(cfg, logx.New(io.Discard))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := svc.Tools(context.Background())
	if len(catalog.External) != 1 {
		t.Fatalf("got %d external sources, want 1", len(catalog.External))
	}
	names := make([]string, 0, len(catalog.External[0].Tools))
	for _, info := range catalog.External[0].Tools {
		names = append(names, info.Name)
	}
	if len(names) != 1 || names[0] != "browser_navigate" {
		t.Errorf("catalog tools = %v, want [browser_navigate]", names)
	}
}

// An external server that does not answer must not break the catalog: the
// source carries the error and everything else keeps working.
func TestToolsCatalogExternalUnreachable(t *testing.T) {
	svc, err := New(catalogConfig(MCPServer{Name: "dead", URL: "http://127.0.0.1:1/mcp"}), logx.New(io.Discard))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := svc.Tools(context.Background())
	if len(catalog.External) != 1 {
		t.Fatalf("got %d external sources, want 1", len(catalog.External))
	}
	if catalog.External[0].Error == "" {
		t.Errorf("unreachable server reported no error")
	}
	if len(catalog.Builtin) == 0 || len(catalog.Builtin[0].Tools) != 4 {
		t.Errorf("builtin report tools lost: %+v", catalog.Builtin)
	}
}
