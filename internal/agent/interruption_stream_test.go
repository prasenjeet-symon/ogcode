package agent

import (
	"errors"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestClassifyInterruption_StreamReadFailures pins the classification of the
// provider layer's mid-stream failures. These reach the loop as plain strings,
// so a wording change on either side must keep them out of the generic bucket:
// the reason is what a resume decides from, and what the UI tells the user.
func TestClassifyInterruption_StreamReadFailures(t *testing.T) {
	tests := []struct {
		name          string
		err           string
		wantReason    session.InterruptReason
		wantResumable bool
	}{
		{
			name:          "stalled connection",
			err:           "stream read failed: no data received for 2m0s, connection appears stalled",
			wantReason:    session.InterruptNetwork,
			wantResumable: true,
		},
		{
			name:          "truncated response",
			err:           "stream read failed: provider closed the connection mid-response",
			wantReason:    session.InterruptNetwork,
			wantResumable: true,
		},
		{
			name:          "over-long SSE line is the provider's shape, not a blip",
			err:           "stream read failed: provider sent a single SSE line larger than 16 MB",
			wantReason:    session.InterruptFatal,
			wantResumable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyInterruption(errors.New(tt.err), 3)
			if got == nil {
				t.Fatal("classifyInterruption returned nil for a non-nil error")
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Resumable != tt.wantResumable {
				t.Errorf("Resumable = %v, want %v", got.Resumable, tt.wantResumable)
			}
			if got.Step != 3 {
				t.Errorf("Step = %d, want 3", got.Step)
			}
		})
	}
}
