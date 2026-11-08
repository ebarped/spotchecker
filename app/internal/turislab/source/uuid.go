// Package source - UUID helper using crypto/rand (stdlib only).
package source

import (
	"crypto/rand"
	"fmt"
)

// uuidV4 generates a random UUID version 4 using crypto/rand.
func uuidV4() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is a system-level problem; panic is appropriate.
		panic("source: crypto/rand unavailable: " + err.Error())
	}
	// Set version (4) and variant bits (RFC 4122).
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10xx

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:],
	)
}
