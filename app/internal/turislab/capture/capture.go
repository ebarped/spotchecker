// Package capture takes website screenshots for scans and the chat using the
// agent-browser CLI. It is deliberately thin: navigation and PNG writing
// belong to agent-browser; this package wires them into a scan and keeps
// calls from stepping on each other.
package capture

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/artifacts"
)

// Config configures the screenshotter.
type Config struct {
	// Command is the agent-browser executable and fixed leading args.
	// Empty auto-detects "agent-browser" in PATH or falls back to npx.
	Command []string
	// Session is the agent-browser session name.
	Session string
	// ExecutablePath sets AGENT_BROWSER_EXECUTABLE_PATH for the CLI
	// (e.g. /usr/bin/chromium).
	ExecutablePath string
	// ArtifactsDir is the folder where PNGs are written.
	ArtifactsDir string
	// Timeout bounds one capture.
	Timeout time.Duration
	// MinInterval skips a recapture of the same id within this window.
	// Zero captures on every call.
	MinInterval time.Duration
}

// Capture implements scan.Screenshoter. One instance is shared by the scanner
// and the chat, so the mutex serializes access to the browser session.
type Capture struct {
	cfg  Config
	mu   sync.Mutex
	last map[string]time.Time
}

func New(cfg Config) *Capture {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if len(cfg.Command) == 0 {
		cfg.Command = defaultCommand()
	}
	if cfg.Session == "" {
		cfg.Session = "turislab-capture"
	}
	return &Capture{cfg: cfg, last: make(map[string]time.Time)}
}

func defaultCommand() []string {
	if p, err := exec.LookPath("agent-browser"); err == nil {
		return []string{p}
	}
	return []string{"npx", "-y", "agent-browser@latest"}
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

	if err := c.run(ctx, "open", websiteURL); err != nil {
		return "", fmt.Errorf("agent-browser open: %w", err)
	}
	if err := c.run(ctx, "screenshot", file); err != nil {
		return "", fmt.Errorf("agent-browser screenshot: %w", err)
	}

	c.last[id] = time.Now()
	return file, nil
}

func (c *Capture) run(ctx context.Context, args ...string) error {
	if len(c.cfg.Command) == 0 {
		return fmt.Errorf("agent-browser command is empty")
	}
	cmdArgs := append([]string{}, c.cfg.Command[1:]...)
	if c.cfg.Session != "" {
		cmdArgs = append(cmdArgs, "--session", c.cfg.Session)
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, c.cfg.Command[0], cmdArgs...)
	if c.cfg.ExecutablePath != "" {
		cmd.Env = append(cmd.Environ(), "AGENT_BROWSER_EXECUTABLE_PATH="+c.cfg.ExecutablePath)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
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
