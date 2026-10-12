//go:build !darwin && !windows

package search

// NewBrowserBackend names the browser-driven backend for this platform. One
// exists only on macOS (Safari, driven through AppleScript) and on Windows
// (Chrome or Edge, driven through Chromium's headless CLI); everywhere else
// there is no browser backend, so this returns nil — which NewFallbackBackend
// collapses to the native HTTP path.
func NewBrowserBackend() Backend { return nil }
