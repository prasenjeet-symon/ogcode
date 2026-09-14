package auth

import (
	"fmt"
	"strings"
)

// ValidateUsername rejects usernames the session-cookie payload cannot carry
// safely. The accounts-mode payload is "expiry|userID[|wsFP]" split on "|", so
// a username containing "|" would corrupt the parse; whitespace-only or
// oversized names are a usability guard, not a security one.
func ValidateUsername(username string) error {
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("username is required")
	}
	if strings.Contains(username, "|") {
		return fmt.Errorf("username must not contain %q", "|")
	}
	if strings.ContainsAny(username, "\r\n") {
		return fmt.Errorf("username must not contain newlines")
	}
	if len(username) > 64 {
		return fmt.Errorf("username must be at most 64 bytes")
	}
	return nil
}
