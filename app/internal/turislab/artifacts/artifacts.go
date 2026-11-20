// Package artifacts resolves the runtime files produced during scans and chat
// turns (currently website screenshots). It owns the filename convention so
// the writer (browser MCP) and the reader (HTTP API) agree on it.
package artifacts

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ScreenshotPath returns the PNG path for an establishment. The id is the only
// part of the path that comes from the request, so it is validated first:
// slashes, ".." and odd characters must never escape artifacts_dir.
func ScreenshotPath(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("invalid establishment id %q", id)
	}
	if dir == "" {
		return "", fmt.Errorf("artifacts dir is empty")
	}
	// The browser runs with its own working directory: a relative filename
	// would be resolved there and the PNG would land where nobody reads it.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve artifacts dir: %w", err)
	}
	return filepath.Join(absDir, id+".png"), nil
}

// ValidID accepts the id shapes our datasets produce (official REAT
// signatures such as R-LU-000710 and generated gen-… ones) and nothing that
// could address another file.
func ValidID(id string) bool {
	if id == "" || len(id) > 128 || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}
