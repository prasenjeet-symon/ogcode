package board

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ValidateHandle reports whether name is a handle the board accepts: lowercase
// letters, digits, dots, underscores and dashes, starting with a letter or
// digit, at most 32 characters. The reserved handles the user and the board
// answer to are refused, because accepting one would let an agent impersonate
// them.
func ValidateHandle(name string) error {
	if name == "" {
		return errors.New("handle is empty")
	}
	if ReservedHandles[name] {
		return fmt.Errorf("%q is a reserved handle — pick another name", name)
	}
	if len(name) > 32 {
		return fmt.Errorf("handle %q is longer than 32 characters", name)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return fmt.Errorf("handle %q must be lowercase letters, digits, dots, underscores or dashes, and start with a letter or digit", name)
		}
	}
	return nil
}

// Slug reduces a free-form name — a teammate's requested name, typically a
// phrase — to something ValidateHandle accepts.
func Slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 24 {
		s = strings.Trim(s[:24], "-")
	}
	return s
}

// NewHandle names a fresh agent on the board: the role plus four random hex
// characters, so two sessions of one agent in a directory stay distinct.
func NewHandle(role string) string {
	base := Slug(role)
	if base == "" {
		base = "agent"
	}
	return base + "-" + randomHex(2)
}

// newID is a unique id for a row, so a client can address an agent without
// depending on its handle.
func newID() string {
	return randomHex(8)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail on a supported platform; fall back to the
		// clock rather than panic in a tool call.
		return fmt.Sprintf("%0*x", n*2, time.Now().UnixNano()&0xffffffff)[:n*2]
	}
	return hex.EncodeToString(buf)
}
