// Package privacy turns platform identifiers into per-session pseudonyms.
// Original author IDs never leave this package's callers in stored form.
package privacy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// NewSalt returns a random per-session secret.
func NewSalt() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return b, nil
}

// AuthorKey is a stable pseudonym for authorID within one session
// (HMAC-SHA256 with the session salt, 24 hex characters). Empty in, empty out.
func AuthorKey(salt []byte, authorID string) string {
	if authorID == "" {
		return ""
	}
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(authorID))
	return hex.EncodeToString(m.Sum(nil))[:24]
}

// EventID identifies a message within a session so overlapping pages do not
// duplicate it. occurrence counts identical (offset, author, text) messages
// in one page, so genuine repeats are kept.
func EventID(salt []byte, offsetMs int64, authorID, text string, occurrence int) string {
	var author any
	if authorID != "" {
		author = authorID
	}
	fp, _ := json.Marshal([]any{offsetMs, author, text, occurrence})
	h := sha256.New()
	h.Write([]byte(hex.EncodeToString(salt)))
	h.Write(fp)
	return hex.EncodeToString(h.Sum(nil))[:32]
}
