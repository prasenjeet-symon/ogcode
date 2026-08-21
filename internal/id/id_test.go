package id

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

// Ids are the sort key: the store returns messages ordered by id, so creation
// order and sort order have to be the same thing. A ULID only sorts by time
// through its 48-bit millisecond prefix, and ids minted inside one millisecond
// share that prefix — which leaves the ordering entirely to the entropy that
// follows it.
//
// Before monotonic entropy this failed about half the time on same-millisecond
// pairs, which in a tight loop is nearly every pair.
func TestNewULID_StrictlyIncreasingWithinAMillisecond(t *testing.T) {
	const n = 20000
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(NewMessageID())
	}

	sameMs := 0
	for i := 1; i < n; i++ {
		// Chars 4..14 are the timestamp; the "msg_" prefix occupies 0..4.
		if ids[i][4:14] == ids[i-1][4:14] {
			sameMs++
		}
		if ids[i] <= ids[i-1] {
			t.Fatalf("id %d sorts at or before its predecessor:\n  %s\n  %s", i, ids[i-1], ids[i])
		}
	}

	// Guard the guard. If the loop were slow enough that every id landed in its
	// own millisecond, the test above would pass without exercising anything.
	if sameMs < n/2 {
		t.Errorf("only %d of %d consecutive pairs shared a millisecond; "+
			"this run did not exercise the tie-break the test exists for", sameMs, n-1)
	}
}

// The property the store actually depends on, stated the way it uses it.
func TestNewULID_SortOrderMatchesCreationOrder(t *testing.T) {
	ids := make([]string, 5000)
	for i := range ids {
		ids[i] = string(NewMessageID())
	}

	shuffled := append([]string(nil), ids...)
	// A deterministic scramble — no randomness needed to prove sorting works.
	for i := range shuffled {
		j := (i * 7919) % len(shuffled)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	sort.Strings(shuffled)

	for i := range ids {
		if shuffled[i] != ids[i] {
			t.Fatalf("sorting did not recover creation order at %d:\n  created %s\n  sorted  %s", i, ids[i], shuffled[i])
		}
	}
}

// The monotonic reader carries the previous id forward, so it is stateful in a
// way the plain source was not. Concurrent callers must not interleave inside
// it — and must not produce a duplicate, which is what a lost update would look
// like from the outside.
func TestNewULID_ConcurrentCallersGetDistinctIDs(t *testing.T) {
	const goroutines, each = 16, 500

	var wg sync.WaitGroup
	out := make(chan string, goroutines*each)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				out <- string(NewMessageID())
			}
		}()
	}
	wg.Wait()
	close(out)

	seen := make(map[string]bool, goroutines*each)
	for id := range out {
		if seen[id] {
			t.Fatalf("duplicate id under concurrency: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != goroutines*each {
		t.Errorf("got %d distinct ids, want %d", len(seen), goroutines*each)
	}
}

// Every id kind keeps its prefix and its length. The prefixes are what make an
// id readable in a log line, and the store slices ids by position.
func TestIDPrefixesAndShape(t *testing.T) {
	for _, tc := range []struct{ prefix, got string }{
		{"ses_", string(NewSessionID())},
		{"msg_", string(NewMessageID())},
		{"prt_", string(NewPartID())},
		{"prm_", string(NewPermissionID())},
		{"pln_", string(NewPlanID())},
		{"tsk_", string(NewTaskID())},
		{"nte_", NewNoteID()},
		{"ntv_", NewNoteVersionID()},
	} {
		if !strings.HasPrefix(tc.got, tc.prefix) {
			t.Errorf("%q does not start with %q", tc.got, tc.prefix)
		}
		// 26 characters of ULID after the four-character prefix.
		if len(tc.got) != len(tc.prefix)+26 {
			t.Errorf("%q is %d chars, want %d", tc.got, len(tc.got), len(tc.prefix)+26)
		}
	}
}
