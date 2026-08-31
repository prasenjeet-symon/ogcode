//go:build !darwin

package search

import "errors"

// errSafariUnavailable mirrors the darwin definition in safari.go: the error
// text is asserted on by platform-neutral tests (TestFallbackBackend_
// FetchPageReportsBothFailures), so the sentinel must exist on every platform
// even though no code path here can return it.
var errSafariUnavailable = errors.New("safari: the browser cannot be driven on this machine")

// Safari automation is macOS-only. On every other platform there is no browser
// fallback to offer, so this returns nil — which NewFallbackBackend reads as
// "no fallback" and collapses to the primary. Callers therefore wire the chain
// the same way everywhere, and the platform question is answered here rather
// than behind a build tag at every call site.
func NewSafariBackend() Backend { return nil }
