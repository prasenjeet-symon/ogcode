package tool

import (
	"context"
	"errors"
	"testing"
)

// TestDeepSearchRecord pins the tool→loop contract behind the session-memory
// note: a search that produced an answer — fresh or replayed from the web
// cache — reports its query so the turn can remember it, while a pipeline error
// or an empty answer reports nothing and is never remembered as research.
func TestDeepSearchRecord(t *testing.T) {
	in := deepSearchInput{query: "what is ogcode", model: "ogx-free", provider: "ogx"}
	dir := t.TempDir()

	t.Run("fresh answer reports its query", func(t *testing.T) {
		counter := &deepSearchCounter{}
		tool := DeepSearchTool{Run: counter.run, Cache: deepSearchTestCache(t)}
		res, err := tool.Execute(context.Background(), in.args(t), in.toolContext(dir))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		query, cached, ok := DeepSearchRecord(res)
		if !ok || query != in.query || cached {
			t.Fatalf("DeepSearchRecord = (%q, cached %v, ok %v), want (%q, false, true)", query, cached, ok, in.query)
		}
	})

	t.Run("replayed answer reports itself as cached", func(t *testing.T) {
		counter := &deepSearchCounter{}
		tool := DeepSearchTool{Run: counter.run, Cache: deepSearchTestCache(t)}
		if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext(dir)); err != nil {
			t.Fatalf("first Execute: %v", err)
		}
		res, err := tool.Execute(context.Background(), in.args(t), in.toolContext(dir))
		if err != nil {
			t.Fatalf("second Execute: %v", err)
		}
		if got := counter.count(); got != 1 {
			t.Fatalf("Run calls = %d, want 1 (the second call must hit the cache)", got)
		}
		query, cached, ok := DeepSearchRecord(res)
		if !ok || query != in.query || !cached {
			t.Fatalf("DeepSearchRecord = (%q, cached %v, ok %v), want (%q, true, true)", query, cached, ok, in.query)
		}
	})

	for _, tc := range []struct {
		name    string
		counter *deepSearchCounter
	}{
		{name: "a pipeline error is not recorded", counter: &deepSearchCounter{errs: []error{errors.New("pipeline exploded")}}},
		{name: "an empty answer is not recorded", counter: &deepSearchCounter{empty: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := DeepSearchTool{Run: tc.counter.run, Cache: deepSearchTestCache(t)}
			res, err := tool.Execute(context.Background(), in.args(t), in.toolContext(dir))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if query, cached, ok := DeepSearchRecord(res); ok {
				t.Fatalf("DeepSearchRecord = (%q, cached %v, ok true), want ok false", query, cached)
			}
		})
	}
}
