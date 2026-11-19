// Package capture takes website screenshots through the Playwright MCP
// sidecar. It is deliberately thin: the browser, the navigation and the PNG
// writing all belong to the MCP server; this package only wires them into a
// scan and keeps the calls from stepping on each other.
package capture

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/datadope/turislab-doctor/internal/turislab/artifacts"
)

// Config configures the MCP-backed screenshotter.
type Config struct {
	// MCPURL is the streamable HTTP endpoint of Playwright MCP.
	MCPURL string
	// ArtifactsDir is the absolute folder where the MCP writes the PNGs.
	ArtifactsDir string
	// Timeout bounds one capture, MCP connection included.
	Timeout time.Duration
	// MinInterval skips a recapture of the same id within this window.
	// Zero captures on every call.
	MinInterval time.Duration
}

// Capture implements scan.Screenshoter. One instance is shared by the scanner
// and the chat, so the mutex serializes access to the single browser session.
type Capture struct {
	cfg  Config
	mu   sync.Mutex
	last map[string]time.Time
}

func New(cfg Config) *Capture {
	if cfg.MCPURL == "" {
		cfg.MCPURL = "http://127.0.0.1:8931/mcp"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Capture{cfg: cfg, last: make(map[string]time.Time)}
}

// Capture navigates to websiteURL and saves <ArtifactsDir>/<id>.png. It returns
// the file path, or an empty path when MinInterval skipped the capture.
func (c *Capture) Capture(ctx context.Context, id, websiteURL string) (string, error) {
	file, err := artifacts.ScreenshotPath(c.cfg.ArtifactsDir, id)
	if err != nil {
		return "", err
	}
	if err := validateWebsite(websiteURL); err != nil {
		return "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.last[id]; ok && c.cfg.MinInterval > 0 && time.Since(last) < c.cfg.MinInterval {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	transport := &mcpgo.StreamableClientTransport{Endpoint: c.cfg.MCPURL}
	client := mcpgo.NewClient(&mcpgo.Implementation{Name: "turislab-capture", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return "", fmt.Errorf("connect to browser MCP: %w", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcpgo.CallToolParams{
		Name:      "browser_navigate",
		Arguments: map[string]any{"url": websiteURL},
	})
	if err != nil {
		return "", fmt.Errorf("browser_navigate: %w", err)
	}
	if err := toolError(res, "browser_navigate"); err != nil {
		return "", err
	}

	// An absolute filename is what the spike verified with --ignore-roots:
	// relative names resolve against the MCP server's working directory.
	res, err = session.CallTool(ctx, &mcpgo.CallToolParams{
		Name:      "browser_take_screenshot",
		Arguments: map[string]any{"filename": file},
	})
	if err != nil {
		return "", fmt.Errorf("browser_take_screenshot: %w", err)
	}
	if err := toolError(res, "browser_take_screenshot"); err != nil {
		return "", err
	}

	c.last[id] = time.Now()
	return file, nil
}

// toolError turns an MCP tool's isError result into a Go error. Playwright MCP
// reports navigation failures this way rather than as protocol errors.
func toolError(res *mcpgo.CallToolResult, tool string) error {
	if res == nil {
		return fmt.Errorf("%s: empty result", tool)
	}
	if !res.IsError {
		return nil
	}
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcpgo.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	if msg := strings.TrimSpace(b.String()); msg != "" {
		return fmt.Errorf("%s: %s", tool, msg)
	}
	return fmt.Errorf("%s: tool reported an error", tool)
}

// validateWebsite keeps the scanner from pointing the browser at internal
// services. Only public http/https URLs are captured.
func validateWebsite(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid website URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid website URL %q: want an absolute http(s) URL", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("invalid website URL %q: local hosts are not captured", raw)
	}
	if ip := net.ParseIP(host); ip != nil &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("invalid website URL %q: private hosts are not captured", raw)
	}
	return nil
}
