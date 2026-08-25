package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// newUUIDv7 generates a UUIDv7 per RFC 9562: the first 48 bits hold the
// Unix-epoch timestamp in milliseconds, the version nibble is 7, the
// variant bits are 10, and the remaining 74 bits are random. Pi session
// ids use the same scheme, so smidja session ids are interchangeable with
// Pi's. Implemented locally on crypto/rand with no external dependencies.
func newUUIDv7() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: read random bytes: %w", err)
	}
	ts := uint64(time.Now().UnixMilli())
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	b[6] = 0x70 | (b[6] & 0x0f) // version 7
	b[8] = 0x80 | (b[8] & 0x3f) // variant 10xx
	return formatUUID(b), nil
}

// fullUUID returns a random UUIDv4 string. It is the entry-id fallback
// after repeated short-id collisions, mirroring Pi's generateId fallback
// to a full randomUUID.
func fullUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: read random bytes: %w", err)
	}
	b[6] = 0x40 | (b[6] & 0x0f) // version 4
	b[8] = 0x80 | (b[8] & 0x3f) // variant 10xx
	return formatUUID(b), nil
}

// formatUUID renders 16 bytes as a lowercase, hyphenated UUID string.
func formatUUID(b [16]byte) string {
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// shortID draws 4 random bytes from crypto/rand and returns them as 8
// lowercase hex characters, the same shape Pi uses for entry ids (the
// first 8 hex characters of a random UUID, that is 32 bits of randomness).
func shortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: read random bytes: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
