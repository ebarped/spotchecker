package source

import (
	"regexp"
	"testing"
)

// uuidRe matches the canonical lowercase UUID v4 text representation.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUIDV4Format(t *testing.T) {
	for i := 0; i < 50; i++ {
		id := uuidV4()
		if !uuidRe.MatchString(id) {
			t.Fatalf("uuidV4() = %q, not a valid UUID v4 (iteration %d)", id, i)
		}
	}
}

func TestUUIDV4Unique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := uuidV4()
		if seen[id] {
			t.Fatalf("duplicate UUID %q after %d draws", id, i)
		}
		seen[id] = true
	}
}
