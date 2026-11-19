package artifacts

import (
	"path/filepath"
	"testing"
)

func TestScreenshotPathIsAbsoluteForTheBrowserSidecar(t *testing.T) {
	got, err := ScreenshotPath("artifacts", "R-LU-000710")
	if err != nil {
		t.Fatalf("ScreenshotPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("path = %q, want an absolute path", got)
	}
	if want := filepath.Join("artifacts", "R-LU-000710.png"); filepath.Base(got) != filepath.Base(want) {
		t.Errorf("path = %q, want the screenshot filename from %q", got, want)
	}
}

func TestScreenshotPathRejectsTraversal(t *testing.T) {
	for _, id := range []string{"", "../outside", "a/b", "a\\b", ".."} {
		if _, err := ScreenshotPath(t.TempDir(), id); err == nil {
			t.Errorf("ScreenshotPath(%q) succeeded, want an error", id)
		}
	}
}
