package agent

import (
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// The loop's backstop caps every tool result that does not declare itself
// bounded. read and file_map return their content whole on purpose and set
// Uncapped; the exemption must be honoured, or a whole-file read would be
// silently clipped on its way to the model — the exact cap the tool no longer
// applies itself.
func TestCapToolOutputHonoursUncapped(t *testing.T) {
	huge := strings.Repeat("x", tool.MaxToolOutputBytes*2)

	got := capToolOutput(tool.Result{Output: huge, Uncapped: true})
	if got.Output != huge {
		t.Errorf("an uncapped result was cut to %d bytes, want %d", len(got.Output), len(huge))
	}
	if got.Truncated {
		t.Error("an uncapped result must not be reported truncated")
	}

	capped := capToolOutput(tool.Result{Output: huge})
	if len(capped.Output) >= len(huge) || !capped.Truncated {
		t.Error("an ordinary result must still be capped by the backstop")
	}
}
