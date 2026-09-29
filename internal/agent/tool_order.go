package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// fileTools names the built-in tools that act on exactly one file, named by
// their "path" argument, and whether each changes it.
var fileTools = map[string]bool{
	"read":         false,
	"file_map":     false,
	"check_syntax": false,
	"edit":         true,
	"write":        true,
}

// sameFileWaits says, for each call in a batch, which earlier calls in the same
// batch it must wait for, so that calls on one file run in the order the model
// wrote them while everything else still runs at once.
//
// A change waits for every call before it on the file; a look — read, file_map,
// check_syntax — waits only for the changes before it, so several reads of one
// file still run together. Without this the batch's order on a file was
// whichever goroutine ran first: a check_syntax written after an edit could
// parse the file from before it, a read could return the old lines, and two
// edits applied in whichever order took the file's lock. Every wait points at
// an earlier call, so the waits can never form a cycle.
//
// Calls whose file cannot be known — bash, grep, glob, anything else — wait for
// nothing, exactly as before; the prompt tells the model to give a shell
// command that changes files a block of its own.
func sameFileWaits(calls []pendingToolCall, workDir string) [][]int {
	type seen struct {
		idx     int
		mutates bool
	}
	waits := make([][]int, len(calls))
	byFile := make(map[string][]seen)
	for i, tc := range calls {
		mutates, ok := fileTools[tc.Name]
		if !ok {
			continue
		}
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(tc.Input, &args) != nil || strings.TrimSpace(args.Path) == "" {
			continue
		}
		key := tool.FileKey(workDir, args.Path)
		for _, earlier := range byFile[key] {
			if mutates || earlier.mutates {
				waits[i] = append(waits[i], earlier.idx)
			}
		}
		byFile[key] = append(byFile[key], seen{i, mutates})
	}
	return waits
}

// runInFileOrder calls run once per index, all concurrently, except that index
// i starts only after every index in waits[i] has finished — or ctx is done,
// in which case it starts at once and meets the cancelled context itself. run
// must recover its own panics; the call is marked finished either way.
func runInFileOrder(ctx context.Context, waits [][]int, run func(i int)) {
	done := make([]chan struct{}, len(waits))
	for i := range done {
		done[i] = make(chan struct{})
	}
	var wg sync.WaitGroup
	for i := range waits {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer close(done[i])
			for _, j := range waits[i] {
				select {
				case <-done[j]:
				case <-ctx.Done():
				}
			}
			run(i)
		}(i)
	}
	wg.Wait()
}
