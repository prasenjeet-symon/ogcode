package nativehost

import (
	"crypto/sha256"
	"path/filepath"
)

// ExtensionIDForPath computes the extension id Chrome assigns to an unpacked
// extension loaded from path: SHA-256 over the absolute path's UTF-8 bytes,
// the first 16 digest bytes, each rendered in the a–p alphabet (high nibble,
// then low) as one 32-character id. It mirrors Chromium's GenerateIdForPath,
// which hashes the path AS SPELLED — verified against Chrome 156.0.8078.48 on
// macOS 15.5, which recorded the sha256 of the mixed-case path verbatim. The
// lowercased spelling this file hashed until 2026-10-25 produced an id Chrome
// never assigned, so the boot-written manifest allowlisted an origin no
// installed extension could launch the host from.
func ExtensionIDForPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return extensionID(abs)
}

// extensionID is the raw id derivation: hash the string, keep the first half
// of the digest, and render every nibble as 'a' + its 0–15 value. (Chromium
// additionally normalizes path separators on Windows; the manifest writer
// refuses Windows anyway, so the path is used as spelled.)
func extensionID(s string) string {
	digest := sha256.Sum256([]byte(s))
	id := make([]byte, 0, 32)
	for _, b := range digest[:16] {
		id = append(id, byte('a')+byte(b>>4), byte('a')+byte(b&0x0f))
	}
	return string(id)
}
