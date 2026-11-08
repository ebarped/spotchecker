package agent

// The tool catalog behind the UI sidebar: which tools the agent carries,
// grouped by origin. Built-in covers the local scan report helpers;
// external covers every MCP server configured by URL.

import (
	"context"
	"net/http"
	"time"

	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolInfo describes one tool of the catalog.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolSource is one origin of tools: the report helpers or one external
// MCP server.
type ToolSource struct {
	Key   string     `json:"key"`
	Label string     `json:"label"`
	Tools []ToolInfo `json:"tools"`
	// Error explains why the tools of this source are unknown, e.g. an
	// external MCP server that does not answer.
	Error string `json:"error,omitempty"`
}

// ToolCatalog is the payload of GET /api/agent/tools.
type ToolCatalog struct {
	Builtin  []ToolSource `json:"builtin"`
	External []ToolSource `json:"external"`
}

// toolListTimeout bounds the listing of one external MCP server, so a dead
// endpoint cannot stall the catalog.
const toolListTimeout = 5 * time.Second

// Tools builds the tool catalog for the sidebar. Both groups are always
// arrays in the JSON, even when empty, so clients need no null handling.
func (s *Service) Tools(ctx context.Context) ToolCatalog {
	catalog := ToolCatalog{Builtin: []ToolSource{}, External: []ToolSource{}}

	reportTools := []ToolInfo{}
	for _, t := range buildTools(&s.report) {
		reportTools = append(reportTools, ToolInfo{Name: t.Name(), Description: t.Description()})
	}
	catalog.Builtin = append(catalog.Builtin, ToolSource{
		Key:   "report",
		Label: "Scan report",
		Tools: reportTools,
	})

	for _, server := range s.cfg.MCP {
		source := ToolSource{Key: "mcp:" + server.Name, Label: server.Name, Tools: []ToolInfo{}}
		tools, err := listMCPTools(ctx, server.URL, server.Headers)
		if err != nil {
			source.Error = err.Error()
		} else {
			source.Tools = tools
		}
		catalog.External = append(catalog.External, source)
	}
	return catalog
}

// HandleTools serves GET /api/agent/tools.
func (s *Service) HandleTools(w http.ResponseWriter, r *http.Request) {
	writeAgentJSON(w, http.StatusOK, s.Tools(r.Context()))
}

// listMCPTools connects to an external streamable HTTP MCP server and lists
// its tools.
func listMCPTools(ctx context.Context, endpoint string, headers map[string]string) ([]ToolInfo, error) {
	transport := &mcpgo.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: newHeaderTransport(headers)},
	}
	tools, err := listMCPToolsFromTransport(ctx, transport)
	if err != nil {
		return nil, err
	}
	return tools, nil
}

// listMCPToolsFromTransport connects an MCP client over the given transport
// (in-memory or HTTP) and returns its tool list.
func listMCPToolsFromTransport(ctx context.Context, transport mcpgo.Transport) ([]ToolInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, toolListTimeout)
	defer cancel()

	client := mcpgo.NewClient(&mcpgo.Implementation{Name: appName, Version: "0.1.0"}, nil)
	session, err := client.Connect(cctx, transport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	response, err := session.ListTools(cctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ToolInfo, 0, len(response.Tools))
	for _, t := range response.Tools {
		out = append(out, ToolInfo{Name: t.Name, Description: t.Description})
	}
	return out, nil
}
