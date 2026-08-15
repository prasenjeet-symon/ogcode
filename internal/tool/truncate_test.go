package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateOutput_NoTruncationWhenSmall(t *testing.T) {
	in := "line1\nline2\nline3"
	out, truncated := TruncateOutput(in, KeepHead)
	if truncated {
		t.Fatalf("small input should not be truncated")
	}
	if out != in {
		t.Fatalf("small input should be unchanged, got %q", out)
	}
}

func TestTruncateOutput_LineCountKeepHead(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxToolOutputLines+500; i++ {
		b.WriteString("x\n")
	}
	out, truncated := TruncateOutput(b.String(), KeepHead)
	if !truncated {
		t.Fatalf("expected truncation")
	}
	lines := strings.Split(out, "\n")
	// kept MaxToolOutputLines + the blank line + notice line(s); ensure we didn't
	// keep everything.
	if len(lines) > MaxToolOutputLines+5 {
		t.Fatalf("kept too many lines: %d", len(lines))
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("expected truncation notice, got tail: %q", out[len(out)-120:])
	}
}

func TestTruncateOutput_KeepTailKeepsEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxToolOutputLines+10; i++ {
		b.WriteString("filler\n")
	}
	b.WriteString("FINAL_ERROR_MARKER")
	out, truncated := TruncateOutput(b.String(), KeepTail)
	if !truncated {
		t.Fatalf("expected truncation")
	}
	if !strings.Contains(out, "FINAL_ERROR_MARKER") {
		t.Fatalf("KeepTail must preserve the end of the output")
	}
	if !strings.HasPrefix(out, "[output truncated") {
		t.Fatalf("KeepTail notice should lead, got: %q", out[:60])
	}
}

func TestTruncateOutput_ByteCapAndLongLine(t *testing.T) {
	// One enormous single line exceeds both the line cap and the byte cap.
	huge := strings.Repeat("a", MaxToolOutputBytes*2)
	out, truncated := TruncateOutput(huge, KeepHead)
	if !truncated {
		t.Fatalf("expected truncation of a huge line")
	}
	if len(out) > MaxToolOutputBytes+512 {
		t.Fatalf("byte cap not enforced: len=%d", len(out))
	}
}

func TestReadTool_DefaultLimitAndFooter(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 0; i < defaultReadLimit+100; i++ {
		b.WriteString("row\n")
	}
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": f})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatalf("reading a file larger than the default limit should truncate")
	}
	if !strings.Contains(res.Output, "Use offset=") {
		t.Fatalf("expected a continuation footer, got tail: %q", res.Output[len(res.Output)-120:])
	}
	// Line count of the numbered body must not exceed the default window.
	body := res.Output
	if idx := strings.Index(body, "\n\n(Showing"); idx >= 0 {
		body = body[:idx]
	}
	if got := len(strings.Split(body, "\n")); got > defaultReadLimit {
		t.Fatalf("returned %d lines, want <= %d", got, defaultReadLimit)
	}
}

func TestReadTool_SmallFileNotTruncated(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(f, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": f})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated {
		t.Fatalf("small file should not be marked truncated")
	}
	if !strings.Contains(res.Output, "     1\ta") {
		t.Fatalf("expected line-numbered output, got: %q", res.Output)
	}
}
