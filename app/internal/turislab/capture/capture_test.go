package capture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFakeAgentBrowser(t *testing.T, failOpen bool) []string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "agent-browser")
	fail := "0"
	if failOpen {
		fail = "1"
	}
	body := fmt.Sprintf(`#!/bin/sh
set -eu
fail_open=%s
log="${FAKE_AB_LOG:-}"
cmd=""
url=""
file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session) shift 2 ;;
    open) cmd=open; url="${2:-}"; shift 2 ;;
    screenshot) cmd=screenshot; file="${2:-}"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$log" ]; then printf '%%s\n' "$cmd $url$file" >> "$log"; fi
if [ "$cmd" = "open" ]; then
  if [ "$fail_open" = "1" ]; then echo "net::ERR_NAME_NOT_RESOLVED" >&2; exit 1; fi
  exit 0
fi
if [ "$cmd" = "screenshot" ]; then
  printf 'PNG' > "$file"
  exit 0
fi
echo "unknown args" >&2
exit 1
`, fail)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake agent-browser: %v", err)
	}
	return []string{script}
}

func TestCaptureWritesScreenshotAtTheGivenPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_AB_LOG", logPath)

	cap := New(Config{Command: writeFakeAgentBrowser(t, false), ArtifactsDir: dir})
	got, err := cap.Capture(context.Background(), "R-LU-000710", "https://example.com/")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := filepath.Join(dir, "R-LU-000710.png")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "PNG" {
		t.Errorf("screenshot file: data=%q err=%v", data, err)
	}
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if !strings.Contains(log, "open https://example.com/") || !strings.Contains(log, "screenshot "+want) {
		t.Errorf("agent-browser calls = %q", log)
	}
}

func TestCaptureMinIntervalSkipsRecapture(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_AB_LOG", logPath)

	cap := New(Config{Command: writeFakeAgentBrowser(t, false), ArtifactsDir: dir, MinInterval: time.Hour})
	first, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err != nil || first == "" {
		t.Fatalf("first Capture = (%q, %v), want a file path", first, err)
	}
	second, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err != nil || second != "" {
		t.Fatalf("second Capture = (%q, %v), want (\"\", nil)", second, err)
	}
	logBytes, _ := os.ReadFile(logPath)
	if got := strings.Count(string(logBytes), "open "); got != 1 {
		t.Errorf("open calls = %d, want 1; log=%q", got, logBytes)
	}

	// With no interval every call recaptures, overwriting the same file.
	fresh := New(Config{Command: writeFakeAgentBrowser(t, false), ArtifactsDir: dir})
	for range 2 {
		if _, err := fresh.Capture(context.Background(), "rest-001", "https://example.com"); err != nil {
			t.Fatalf("Capture: %v", err)
		}
	}
}

func TestCaptureRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	cap := New(Config{Command: []string{"/bin/false"}, ArtifactsDir: dir})

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
	if _, err := os.Stat(filepath.Join(dir, "rest-001.png")); !os.IsNotExist(err) {
		t.Errorf("screenshot should not be written for rejected input: %v", err)
	}
}

func TestCaptureReportsErrors(t *testing.T) {
	dir := t.TempDir()
	cap := New(Config{Command: writeFakeAgentBrowser(t, true), ArtifactsDir: dir})
	_, err := cap.Capture(context.Background(), "rest-001", "https://example.com")
	if err == nil || !strings.Contains(err.Error(), "ERR_NAME_NOT_RESOLVED") {
		t.Fatalf("Capture error = %v, want navigation failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "rest-001.png")); !os.IsNotExist(statErr) {
		t.Errorf("screenshot should not exist after failed open: %v", statErr)
	}
}
