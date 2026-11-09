// Package uuid generates RFC 9562 version-4 UUIDs, matching util/crypto randomUUID.
// It reads 16 cryptographically random bytes, sets version/variant bits, and
// formats them as 8-4-4-4-12 hexadecimal groups.
package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewV4 generates a random version-4 UUID string.
func NewV4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Set byte 6's high nibble to 0100 and byte 8's high two bits to 10.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}

// MustV4 generates a UUID and panics on failure, for initialization paths requiring success.
func MustV4() string {
	id, err := NewV4()
	if err != nil {
		panic(err)
	}
	return id
}
