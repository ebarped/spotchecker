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
	if len(report.Tools) != 3 {
		t.Fatalf("report tools = %d, want 3", len(report.Tools))
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
// one tool, the way an external MCP server does.
func externalMCPFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcpgo.NewServer(&mcpgo.Implementation{Name: "test-mcp", Version: "0"}, nil)
	mcpgo.AddTool(server, &mcpgo.Tool{Name: "echo", Description: "Echoes whatever it is given"},
		func(ctx context.Context, req *mcpgo.CallToolRequest, input struct{}) (*mcpgo.CallToolResult, struct{}, error) {
			return &mcpgo.CallToolResult{Content: []mcpgo.Content{
				&mcpgo.TextContent{Text: "echo"},
			}}, struct{}{}, nil
		})
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
	if len(catalog.Builtin) == 0 || len(catalog.Builtin[0].Tools) != 3 {
		t.Errorf("builtin report tools lost: %+v", catalog.Builtin)
	}
}
