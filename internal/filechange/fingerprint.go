package filechange

import "os"

// Fingerprint is what the gate remembers of a file's bytes without holding
// them: its size and modification time. It is how a change made by a process
// the gate cannot see is noticed — a shell command that rewrites, truncates,
// moves or deletes a file — by stat'ing the file the gate last saw and
// comparing.
//
// It is deliberately not a content hash. Hashing every tracked file after
// every shell command means reading each of them, and the only question here
// is whether the bytes moved; the agent a refusal stops re-reads the file
// anyway.
type Fingerprint struct {
	Size  int64
	MTime int64 // UnixNano, compared for inequality only
	// Exists is false for a path that could not be stat'd. A file that was
	// there and is not any more is a change like any other, so the zero
	// Fingerprint is the state of a deleted file rather than an unknown one.
	Exists bool
}

// FingerprintOf reads path's fingerprint. A path that cannot be stat'd —
// deleted, or unreadable — is reported as not existing.
func FingerprintOf(path string) Fingerprint {
	info, err := os.Stat(path)
	if err != nil {
		return Fingerprint{}
	}
	return Fingerprint{Size: info.Size(), MTime: info.ModTime().UnixNano(), Exists: true}
}
