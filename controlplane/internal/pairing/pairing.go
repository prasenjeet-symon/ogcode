// Package pairing implements the master's worker authentication: a constant-time
// pairing-secret check and short-lived, rotating worker tokens. Stdlib only — no
// auth framework, per the design's trust-boundary-stays-small principle.
package pairing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"
)

// Authenticator validates the shared pairing secret and mints worker tokens.
type Authenticator struct {
	secret   []byte
	tokenTTL time.Duration
}

// New returns an Authenticator for the given pairing secret and token lifetime.
func New(secret string, tokenTTL time.Duration) *Authenticator {
	if tokenTTL <= 0 {
		tokenTTL = 15 * time.Minute
	}
	return &Authenticator{secret: []byte(secret), tokenTTL: tokenTTL}
}

// CheckSecret reports whether presented equals the configured pairing secret,
// compared in constant time to avoid leaking length/content via timing.
func (a *Authenticator) CheckSecret(presented string) bool {
	// subtle.ConstantTimeCompare returns 0 when lengths differ; guard the empty
	// case so a blank secret never authenticates.
	if len(a.secret) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), a.secret) == 1
}

// Token is an issued worker token and its expiry.
type Token struct {
	Value      string
	ExpiresAt  time.Time
}

// Expired reports whether the token is past its expiry as of now.
func (t Token) Expired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// Mint issues a fresh random worker token valid for the configured TTL.
func (a *Authenticator) Mint(now time.Time) (Token, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, fmt.Errorf("mint token: %w", err)
	}
	return Token{
		Value:     base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt: now.Add(a.tokenTTL),
	}, nil
}

// ShouldRotate reports whether a token is within the last third of its lifetime
// (or already expired), the point at which Heartbeat proactively rotates it.
func (a *Authenticator) ShouldRotate(t Token, now time.Time) bool {
	if t.Value == "" {
		return true
	}
	remaining := t.ExpiresAt.Sub(now)
	return remaining <= a.tokenTTL/3
}
