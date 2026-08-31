//go:build !darwin

package search

// Safari automation is macOS-only. On every other platform there is no browser
// fallback to offer, so this returns nil — which NewFallbackBackend reads as
// "no fallback" and collapses to the primary. Callers therefore wire the chain
// the same way everywhere, and the platform question is answered here rather
// than behind a build tag at every call site.
func NewSafariBackend() Backend { return nil }
