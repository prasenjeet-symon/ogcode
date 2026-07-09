package agent

import (
	"context"
	"strings"
	"testing"
)

func TestLoopControl_PushDrainGuidance(t *testing.T) {
	lc := NewLoopControl()

	// Empty initially
	if lc.DrainGuidance() != "" {
		t.Error("expected empty guidance initially")
	}
	if lc.HasPendingGuidance() {
		t.Error("expected no pending guidance initially")
	}

	// Push and drain
	lc.PushGuidance("stop refactoring tests")
	lc.PushGuidance("just fix the build")

	if !lc.HasPendingGuidance() {
		t.Error("expected pending guidance after push")
	}

	got := lc.DrainGuidance()
	if got == "" {
		t.Fatal("expected non-empty guidance after drain")
	}
	if !strings.Contains(got, "stop refactoring tests") {
		t.Errorf("expected first guidance text in drained result, got %q", got)
	}
	if !strings.Contains(got, "just fix the build") {
		t.Errorf("expected second guidance text in drained result, got %q", got)
	}
	if !strings.Contains(got, "---") {
		t.Error("expected separator between multiple guidance texts")
	}

	// After drain, should be empty again
	if lc.HasPendingGuidance() {
		t.Error("expected no pending guidance after drain")
	}
	if lc.DrainGuidance() != "" {
		t.Error("expected empty guidance after drain")
	}
}

func TestLoopControl_NilSafe(t *testing.T) {
	var lc *LoopControl
	// All methods should be nil-safe
	lc.PushGuidance("hello")
	if lc.DrainGuidance() != "" {
		t.Error("expected empty from nil LoopControl")
	}
	if lc.HasPendingGuidance() {
		t.Error("expected false from nil LoopControl")
	}
	if lc.CancelTool() {
		t.Error("expected false from nil CancelTool")
	}
	lc.SetToolCancel(func() {})
	lc.ClearToolCancel()
}

func TestLoopControl_CancelTool(t *testing.T) {
	lc := NewLoopControl()

	// No tool running — CancelTool returns false
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false when no tool is running")
	}

	// Register a cancel func
	cancelled := false
	lc.SetToolCancel(func() { cancelled = true })

	// CancelTool should call it and return true
	if !lc.CancelTool() {
		t.Error("expected CancelTool to return true when a tool is running")
	}
	if !cancelled {
		t.Error("expected cancel func to be called")
	}

	// After cancel, the stored func is cleared — second call returns false
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false after cancel clears the func")
	}
}

func TestLoopControl_ClearToolCancel(t *testing.T) {
	lc := NewLoopControl()

	cancelled := false
	lc.SetToolCancel(func() { cancelled = true })
	lc.ClearToolCancel()

	// After ClearToolCancel, CancelTool returns false without calling
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false after ClearToolCancel")
	}
	if cancelled {
		t.Error("expected cancel func NOT to be called after ClearToolCancel")
	}
}

func TestWithLoopControl_ContextRoundtrip(t *testing.T) {
	lc := NewLoopControl()
	ctx := context.Background()

	// Without wrapping — returns nil
	if LoopControlFromContext(ctx) != nil {
		t.Error("expected nil LoopControl from un-wrapped context")
	}

	// With wrapping — returns the same pointer
	wrapped := WithLoopControl(ctx, lc)
	got := LoopControlFromContext(wrapped)
	if got != lc {
		t.Error("expected LoopControlFromContext to return the same LoopControl pointer")
	}
}

func TestGuidancePrompt(t *testing.T) {
	got := guidancePrompt("just fix the build")
	if !strings.Contains(got, "just fix the build") {
		t.Error("expected guidance text in prompt")
	}
	if !strings.Contains(got, "<system-reminder>") {
		t.Error("expected system-reminder wrapper")
	}
	if !strings.Contains(got, "</system-reminder>") {
		t.Error("expected closing system-reminder tag")
	}
	if !strings.Contains(got, "new guidance") {
		t.Error("expected guidance context phrase in prompt")
	}
}

// TestLoopControl_PushConcurrent verifies that PushGuidance and DrainGuidance
// are safe for concurrent use (the HTTP handler pushes while the loop drains).
func TestLoopControl_PushConcurrent(t *testing.T) {
	lc := NewLoopControl()
	done := make(chan struct{})

	// Concurrent pusher
	go func() {
		for i := 0; i < 100; i++ {
			lc.PushGuidance("guidance")
		}
		close(done)
	}()

	// Concurrent drainer
	drained := 0
	for i := 0; i < 100; i++ {
		if lc.DrainGuidance() != "" {
			drained++
		}
	}
	<-done

	// Drain whatever is left
	for {
		g := lc.DrainGuidance()
		if g == "" {
			break
		}
		drained++
	}

	// We should have drained roughly 100 times (some pushes may have been
	// coalesced into a single drain, but the total should be > 0).
	if drained == 0 {
		t.Error("expected at least one drain")
	}
}