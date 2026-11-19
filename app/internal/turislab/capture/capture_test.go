package capture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeBrowser is an MCP server with the two Playwright tools the capturer uses.
// The "screenshot" tool writes the file itself, like Playwright MCP does.
type fakeBrowser struct {
	mu          sync.Mutex
	navigated   []string
	files       []string
	navigateErr bool
	shots       int
}

func (f *fakeBrowser) handler() http.Handler {
	srv := mcpgo.NewServer(&mcpgo.Implementation{Name: "fake-playwright", Version: "0.1.0"}, nil)
	mcpgo.AddTool(srv, &mcpgo.Tool{Name: "browser_navigate"},
		func(_ context.Context, _ *mcpgo.CallToolRequest, in struct {
			URL string `json:"url"`
		}) (*mcpgo.CallToolResult, any, error) {
			f.mu.Lock()
			f.navigated = append(f.navigated, in.URL)
			fail := f.navigateErr
			f.mu.Unlock()
			if fail {
				return &mcpgo.CallToolResult{IsError: true, Content: []mcpgo.Content{
					&mcpgo.TextContent{Text: "net::ERR_CONNECTION_REFUSED"},
				}}, nil, nil
			}
			return nil, "ok", nil
		})
	mcpgo.AddTool(srv, &mcpgo.Tool{Name: "browser_take_screenshot"},
		func(_ context.Context, _ *mcpgo.CallToolRequest, in struct {
			Filename string `json:"filename"`
		}) (*mcpgo.CallToolResult, any, error) {
			f.mu.Lock()
			f.files = append(f.files, in.Filename)
			f.shots++
			f.mu.Unlock()
			if err := os.WriteFile(in.Filename, []byte("png bytes"), 0o600); err != nil {
				return nil, nil, err
			}
			return nil, "ok", nil
		})
	return mcpgo.NewStreamableHTTPHandler(func(*http.Request) *mcpgo.Server {
		return srv
	}, &mcpgo.StreamableHTTPOptions{JSONResponse: true})
}

func (f *fakeBrowser) counts() (int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shots, append([]string(nil), f.navigated...), append([]string(nil), f.files...)
}

func TestCaptureWritesScreenshotAtTheGivenPath(t *testing.T) {
	dir := t.TempDir()
	browser := &fakeBrowser{}
	httpSrv := httptest.NewServer(browser.handler())
	defer httpSrv.Close()

	cap := New(Config{MCPURL: httpSrv.URL, ArtifactsDir: dir})
	got, err := cap.Capture(context.Background(), "R-LU-000710", "https://example.com/")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := filepath.Join(dir, "R-LU-000710.png")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "png bytes" {
		t.Errorf("screenshot file: data=%q err=%v", data, err)
	}
	shots, urls, files := browser.counts()
	if shots != 1 || len(urls) != 1 || urls[0] != "https://example.com/" || len(files) != 1 || files[0] != want {
		t.Errorf("MCP calls: shots=%d urls=%q files=%q", shots, urls, files)
	}
}

func TestCaptureMinIntervalSkipsRecapture(t *testing.T) {
	dir := t.TempDir()
	browser := &fakeBrowser{}
	httpSrv := httptest.NewServer(browser.handler())
	defer httpSrv.Close()

	cap := New(Config{MCPURL: httpSrv.URL, ArtifactsDir: dir, MinInterval: time.Hour})
	first, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err != nil || first == "" {
		t.Fatalf("first Capture = (%q, %v), want a file path", first, err)
	}
	second, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err != nil || second != "" {
		t.Fatalf("second Capture = (%q, %v), want (\"\", nil)", second, err)
	}
	if shots, _, _ := browser.counts(); shots != 1 {
		t.Errorf("screenshot tool called %d times, want 1", shots)
	}

	// With no interval every call recaptures, overwriting the same file.
	fresh := New(Config{MCPURL: httpSrv.URL, ArtifactsDir: dir})
	for range 2 {
		if _, err := fresh.Capture(context.Background(), "rest-001", "https://example.com"); err != nil {
			t.Fatalf("Capture: %v", err)
		}
	}
	if shots, _, _ := browser.counts(); shots != 3 {
		t.Errorf("screenshot tool called %d times, want 3", shots)
	}
}

func TestCaptureRejectsBadInput(t *testing.T) {
	browser := &fakeBrowser{}
	httpSrv := httptest.NewServer(browser.handler())
	defer httpSrv.Close()
	cap := New(Config{MCPURL: httpSrv.URL, ArtifactsDir: t.TempDir()})

	for _, tc := range []struct{ id, site string }{
		{"../outside", "https://example.com"},
		{"rest-001", "http://127.0.0.1:8080/admin"},
		{"rest-001", "file:///etc/passwd"},
		{"rest-001", "not a url"},
	} {
		if _, err := cap.Capture(context.Background(), tc.id, tc.site); err == nil {
			t.Errorf("Capture(%q, %q) succeeded, want an error", tc.id, tc.site)
		}
	}
	if shots, _, _ := browser.counts(); shots != 0 {
		t.Errorf("browser touched %d times for rejected input", shots)
	}
}

func TestCaptureReportsToolErrors(t *testing.T) {
	browser := &fakeBrowser{navigateErr: true}
	httpSrv := httptest.NewServer(browser.handler())
	defer httpSrv.Close()
	cap := New(Config{MCPURL: httpSrv.URL, ArtifactsDir: t.TempDir()})

	_, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err == nil || !strings.Contains(err.Error(), "ERR_CONNECTION_REFUSED") {
		t.Fatalf("Capture error = %v, want the browser's message", err)
	}
	if shots, _, _ := browser.counts(); shots != 0 {
		t.Errorf("screenshot tool called after a failed navigation: %d", shots)
	}
}
