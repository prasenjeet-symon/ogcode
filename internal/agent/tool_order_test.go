package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func fileCall(name, path string) pendingToolCall {
	in, _ := json.Marshal(map[string]any{"path": path})
	return pendingToolCall{Name: name, Input: in}
}

// Calls on one file wait for the calls before them that they could collide
// with: a change for every earlier call on the file, a look only for earlier
// changes. Different files, and tools whose file is unknown, wait for nothing.
func TestSameFileWaits(t *testing.T) {
	dir := t.TempDir()
	calls := []pendingToolCall{
		fileCall("edit", "a.go"),                                              // 0
		fileCall("read", "a.go"),                                              // 1: after the edit
		fileCall("read", "b.go"),                                              // 2: another file — free
		fileCall("read", "./a.go"),                                            // 3: the same file spelled differently
		fileCall("write", filepath.Join(dir, "a.go")),                         // 4: after every earlier call on a.go
		{Name: "bash", Input: json.RawMessage(`{"command":"gofmt -w a.go"}`)}, // 5: unknowable — free
		fileCall("check_syntax", "a.go"),                                      // 6: after both changes
		fileCall("grep", "a.go"),                                              // 7: not a single-file tool — free
		{Name: "read", Input: json.RawMessage(`{not json`)},                   // 8: unreadable — free
	}
	got := sameFileWaits(calls, dir)
	want := [][]int{nil, {0}, nil, {0}, {0, 1, 3}, nil, {0, 4}, nil, nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}

	// Reads of one file with no change between them run together.
	if got := sameFileWaits([]pendingToolCall{fileCall("read", "a.go"), fileCall("file_map", "a.go")}, dir); got[0] != nil || got[1] != nil {
		t.Errorf("two looks at one file were ordered: %v", got)
	}
}

// A symlink and its target are one file to the write, so they are one file to
// the ordering too.
func TestSameFileWaitsFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.go")
	if err := os.WriteFile(target, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := sameFileWaits([]pendingToolCall{fileCall("edit", "link.go"), fileCall("read", "real.go")}, dir)
	if !reflect.DeepEqual(got[1], []int{0}) {
		t.Errorf("a read of the target did not wait for an edit through the link: %v", got)
	}
}

// runInFileOrder keeps waiting calls behind the calls they wait for, and
// everything else concurrent: here the edit cannot finish until the call on
// another file has run, which would deadlock if the batch were serialized.
func TestRunInFileOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}
	otherRan := make(chan struct{})
	runInFileOrder(context.Background(), [][]int{nil, {0}, nil}, func(i int) {
		switch i {
		case 0: // edit a.go — finishes only once the other file's call has run
			select {
			case <-otherRan:
			case <-time.After(2 * time.Second):
				t.Error("the call on another file did not run alongside the edit")
			}
			record("edit a.go")
		case 1: // read a.go
			record("read a.go")
		case 2: // read b.go
			record("read b.go")
			close(otherRan)
		}
	})
	if !reflect.DeepEqual(order, []string{"read b.go", "edit a.go", "read a.go"}) {
		t.Errorf("order = %v, want the other file first and the read after the edit", order)
	}
}

// A cancelled turn must not leave a call waiting on one that is itself stuck:
// once the context is done, waiting calls start at once and meet the
// cancellation themselves.
func TestRunInFileOrderStopsWaitingWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	laterRan := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runInFileOrder(ctx, [][]int{nil, {0}}, func(i int) {
			if i == 1 {
				close(laterRan)
				return
			}
			<-laterRan // the first call is stuck until the waiting one runs
		})
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled batch deadlocked on a waiting call")
	}
}
