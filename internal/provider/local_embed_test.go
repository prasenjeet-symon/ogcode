package provider

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The Hugot session is process-lifetime state built on whichever call happens
// to be first. memory.WriteMemory gives every write its own 120s context and
// cancels it on the way out, so a session that captured that context died with
// it and every later embedding in the process failed with "context canceled".
func TestEmbedSurvivesCancelledInitContext(t *testing.T) {
	var emb Embedder = NewLocalEmbedder("")
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		_, err := emb.Embed(ctx, []string{"a short fact about authentication"})
		cancel() // exactly what WriteMemory's deferred cancel does
		if err != nil {
			t.Fatalf("embed #%d failed after an earlier context was cancelled: %v", i, err)
		}
	}
}

// gte-small has a hard 512-position window and the tokenizer does not truncate,
// so over-long input used to fail graph compilation outright. Facts written by
// the agent average ~20 KB, so this was the common case, not the edge case.
func TestEmbedClipsOverlongInput(t *testing.T) {
	var emb Embedder = NewLocalEmbedder("")
	ctx := context.Background()

	// Ordinary prose, far past the window.
	long := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2000)
	v, err := emb.Embed(ctx, []string{long})
	if err != nil {
		t.Fatalf("embedding %d chars of prose: %v", len(long), err)
	}
	if len(v) != 1 || len(v[0]) == 0 {
		t.Fatalf("expected one non-empty vector, got %d vectors", len(v))
	}

	// Worst case: punctuation-dense JSON tokenizes at ~1.0 chars/token, so the
	// initial character budget overflows and only the halving retry saves it.
	dense := strings.Repeat(`{"a":1,"b":"x/y_z-0","c":[2,3]},`, 400)
	if _, err := emb.Embed(ctx, []string{dense}); err != nil {
		t.Fatalf("embedding %d chars of dense JSON: %v", len(dense), err)
	}
}

// A batch is only as embeddable as its longest member.
func TestEmbedClipsPerInputInBatch(t *testing.T) {
	var emb Embedder = NewLocalEmbedder("")
	v, err := emb.Embed(context.Background(), []string{
		"short",
		strings.Repeat("overlong input that will not fit in the window. ", 1000),
		"also short",
	})
	if err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	if len(v) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(v))
	}
	for i, vec := range v {
		if len(vec) == 0 {
			t.Errorf("vector %d is empty", i)
		}
	}
}

// Clipping must not split a multi-byte rune.
func TestClipEmbedInputsRuneBoundary(t *testing.T) {
	in := strings.Repeat("日本語テキスト", 500) // 3 bytes per rune
	out, clipped := clipEmbedInputs([]string{in}, 100)
	if !clipped {
		t.Fatal("expected the input to be clipped")
	}
	if !utf8ValidString(out[0]) {
		t.Fatalf("clipping produced invalid UTF-8: %q", out[0])
	}
	if len(out[0]) > 100 {
		t.Fatalf("clipped to %d bytes, budget was 100", len(out[0]))
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
