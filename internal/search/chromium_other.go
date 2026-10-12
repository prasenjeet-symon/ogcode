//go:build !windows

package search

import "errors"

// errChromiumUnavailable mirrors the windows definition in chromium.go: the
// error text is asserted on by platform-neutral tests, so the sentinel must
// exist on every platform even though no code path here can return it.
var errChromiumUnavailable = errors.New("chromium: the browser cannot be driven on this machine")

// The Chromium backend is Windows-only. On every other platform there is no
// browser of this kind to offer, so this returns nil — which NewFallbackBackend
// reads as "no fallback" and collapses to the primary. Callers therefore wire
// the chain the same way everywhere, and the platform question is answered here
// rather than behind a build tag at every call site.
func NewChromiumBackend() Backend { return nil }
