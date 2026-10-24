package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// changeGate is one project directory with several sessions sharing one change
// log: the shape a server has when an interactive session, a teammate and a
// second session work in the same project. A memory-only Log (nil store) is
// enough here — the gate's decisions are the same, only the history rows are
// missing, and those are covered by internal/filechange's own tests.
type changeGate struct {
	t   *testing.T
	dir string
	log *filechange.Log
}

func newChangeGate(t *testing.T) *changeGate {
	t.Helper()
	return &changeGate{t: t, dir: t.TempDir(), log: filechange.NewLog(context.Background(), nil)}
}

// ctx is the tool context one session runs with: the gate is live and there is
// no board handle, which is the board-free case.
func (g *changeGate) ctx(s session.SessionID) Context {
	return Context{SessionDir: g.dir, SessionID: s, Agent: "build", FileLog: g.log}
}

func (g *changeGate) call(s session.SessionID, run func(context.Context, json.RawMessage, Context) (Result, error), params map[string]any) (Result, error) {
	g.t.Helper()
	args, err := json.Marshal(params)
	if err != nil {
		g.t.Fatalf("marshal args: %v", err)
	}
	return run(context.Background(), args, g.ctx(s))
}

// write runs a write as s, supplying the description the tool now requires
// unless the test is about that requirement.
func (g *changeGate) write(s session.SessionID, params map[string]any) Result {
	g.t.Helper()
	if _, ok := params["description"]; !ok {
		params["description"] = "test change"
	}
	res, err := g.call(s, WriteTool{}.Execute, params)
	if err != nil {
		g.t.Fatalf("write: %v", err)
	}
	return res
}

// edit runs an edit as s, with the same description default as write.
func (g *changeGate) edit(s session.SessionID, params map[string]any) Result {
	g.t.Helper()
	form := asEditsForm(params)
	if _, ok := form["description"]; !ok {
		form["description"] = "test change"
	}
	res, err := g.call(s, EditTool{}.Execute, form)
	if err != nil {
		g.t.Fatalf("edit: %v", err)
	}
	return res
}

func (g *changeGate) read(s session.SessionID, path string) {
	g.t.Helper()
	if _, err := g.call(s, ReadTool{}.Execute, map[string]any{"path": path}); err != nil {
		g.t.Fatalf("read: %v", err)
	}
}

func (g *changeGate) content(path string) string {
	g.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		g.t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestStaleWriteIsRefusedWithTheCatchUp pins the refusal itself: nothing is
// written, and the result carries everything the other session needs to redo
// the change — who changed the file, what they said they were doing, and the
// diff they produced.
func TestStaleWriteIsRefusedWithTheCatchUp(t *testing.T) {
	g := newChangeGate(t)
	// The refusal names the other agent the way the prompt does: its board
	// handle when the server can resolve one.
	g.log.Name = func(id session.SessionID) string {
		if id == "session-b" {
			return "reviewer"
		}
		return string(id)
	}
	path := filepath.Join(g.dir, "notes.md")
	g.write("session-a", map[string]any{"path": path, "content": "one\n"})
	g.read("session-a", path)
	g.write("session-b", map[string]any{
		"path":        path,
		"content":     "two\n",
		"description": "tightened the header",
	})

	res := g.write("session-a", map[string]any{"path": path, "content": "three\n"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	if got := g.content(path); got != "two\n" {
		t.Fatalf("file = %q, want the other session's content %q — a refusal must not write", got, "two\n")
	}
	for _, want := range []string{
		"Nothing was written",
		"revision 1",
		"revision 2",
		"reviewer",
		"tightened the header",
		"- one",
		"+ two",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, res.Output)
		}
	}
}

// TestStaleEditIsRefusedUntilTheFileIsReread covers the other half: an edit
// whose anchors were chosen against content that is gone is refused, and the
// same edit applies once the session has read the current file.
func TestStaleEditIsRefusedUntilTheFileIsReread(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	g.write("session-a", map[string]any{"path": path, "content": "one\n"})
	g.read("session-a", path)
	g.write("session-b", map[string]any{"path": path, "content": "two\n"})

	res := g.edit("session-a", map[string]any{"path": path, "old_string": "two", "new_string": "three"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	if got := g.content(path); got != "two\n" {
		t.Fatalf("file = %q, want %q — a refused edit must not touch the file", got, "two\n")
	}

	// Re-reading is the whole remedy: the bookmark catches up to the current
	// revision and the same edit now lands.
	g.read("session-a", path)
	g.edit("session-a", map[string]any{"path": path, "old_string": "two", "new_string": "three"})
	if got := g.content(path); got != "three\n" {
		t.Fatalf("file after the retry = %q, want %q", got, "three\n")
	}
}

// TestMutationWithoutAReadIsAllowed pins the gate's floor: a session that has
// not read the file has no bookmark, so there is nothing it could have built a
// change against and nothing to refuse.
func TestMutationWithoutAReadIsAllowed(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	g.write("session-a", map[string]any{"path": path, "content": "one\n"})

	// session-c never read the file, so it is not behind anything.
	g.write("session-c", map[string]any{"path": path, "content": "two\n"})
	if got := g.content(path); got != "two\n" {
		t.Fatalf("file = %q, want %q", got, "two\n")
	}
	// The same holds for an unread edit.
	g.edit("session-c", map[string]any{"path": path, "old_string": "two", "new_string": "three"})
	if got := g.content(path); got != "three\n" {
		t.Fatalf("file = %q, want %q", got, "three\n")
	}
}

// TestOwnEarlierChangeIsNotStale pins that a session is never blocked by
// itself: a fix-mistake-retry loop on one file keeps working.
func TestOwnEarlierChangeIsNotStale(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	g.write("session-a", map[string]any{"path": path, "content": "one\n"})
	g.read("session-a", path)
	g.write("session-a", map[string]any{"path": path, "content": "two\n"})

	res := g.edit("session-a", map[string]any{"path": path, "old_string": "two", "new_string": "three"})
	if res.Title == "File changed by another agent" {
		t.Fatalf("a session's own change was reported as another session's:\n%s", res.Output)
	}
	if got := g.content(path); got != "three\n" {
		t.Fatalf("file = %q, want %q", got, "three\n")
	}
}

// TestWriteAndEditRequireADescription pins the argument the catch-up depends
// on: a change with no description leaves the next agent a diff and no intent,
// so the call is refused as a result (not an error) and nothing is written.
func TestWriteAndEditRequireADescription(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "x.txt")

	res, err := g.call("session-a", WriteTool{}.Execute, map[string]any{"path": path, "content": "hi\n"})
	if err != nil {
		t.Fatalf("write without a description returned an error: %v", err)
	}
	if res.Title != "Description required" {
		t.Fatalf("write title = %q, want %q", res.Title, "Description required")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("write without a description touched the disk: stat err = %v", err)
	}

	mustWriteFile(t, path, "hi\n")
	hunk := map[string]any{"edits": []map[string]any{{"old_string": "hi", "new_string": "bye"}}}
	hunk["path"] = path
	res, err = g.call("session-a", EditTool{}.Execute, hunk)
	if err != nil {
		t.Fatalf("edit without a description returned an error: %v", err)
	}
	if res.Title != "Description required" {
		t.Fatalf("edit title = %q, want %q", res.Title, "Description required")
	}
	if got := g.content(path); got != "hi\n" {
		t.Fatalf("edit without a description changed the file to %q", got)
	}

	// Whitespace is not a description either.
	blank := map[string]any{"path": path, "content": "bye\n", "description": "   "}
	res, err = g.call("session-a", WriteTool{}.Execute, blank)
	if err != nil {
		t.Fatalf("write with a blank description returned an error: %v", err)
	}
	if res.Title != "Description required" {
		t.Fatalf("blank description title = %q, want %q", res.Title, "Description required")
	}
	if got := g.content(path); got != "hi\n" {
		t.Fatalf("write with a blank description changed the file to %q", got)
	}
}

// TestMapOnlyReadDoesNotCertifyContent pins the order of the map branch and
// the read bookmark: a read that answers with the file's structural map has
// returned none of the bytes, so it must not stamp the bookmark as though it
// had. The bookmark would otherwise do exactly the harm the gate exists to
// prevent: a change another session made since the last real read gets
// certified away by the map-only re-read, and the next edit built against
// stale content applies over it instead of being refused with the catch-up.
func TestMapOnlyReadDoesNotCertifyContent(t *testing.T) {
	g := newChangeGate(t)
	// A symbolic Go file comfortably past the map threshold, built inside the
	// gate's project directory: exactly the shape a map-only read answers
	// with.
	demoFile := func(n int) string {
		var b strings.Builder
		b.WriteString("package demo\n\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "// Fn%d does a thing.\nfunc Fn%d() int {\n\treturn %d\n}\n\n", i, i, i)
		}
		return b.String()
	}
	path := filepath.Join(g.dir, "big.go")
	mustWriteFile(t, path, demoFile(60))
	key := FileKey(g.dir, path)

	// session-a first reads the whole file through an explicit range: real
	// bytes, so the bookmark is stamped at revision 0.
	first, err := g.call("session-a", ReadTool{}.Execute, map[string]any{
		"path":   path,
		"ranges": []map[string]any{{"start_line": 1, "end_line": 2000}},
	})
	if err != nil {
		t.Fatalf("ranged read: %v", err)
	}
	if strings.Contains(first.Output, "too long to read in full") || !strings.Contains(first.Output, "return 7\n") {
		t.Fatalf("ranged read did not return content (title %q):\n%s", first.Title, truncate(first.Output))
	}
	if seen := g.log.Seen(key, "session-a"); seen != 0 {
		t.Fatalf("content read left bookmark %d, want 0", seen)
	}

	// Another session changes the file: revision 1, content session-a has
	// not seen.
	bVersion := demoFile(60) + "// tightened by session-b\n"
	g.write("session-b", map[string]any{"path": path, "content": bVersion, "description": "session-b's filler"})

	// session-a now re-reads without a range — a long file with mappable
	// declarations, so the answer is the map and none of the bytes.
	second, err := g.call("session-a", ReadTool{}.Execute, map[string]any{"path": path})
	if err != nil {
		t.Fatalf("map-only read: %v", err)
	}
	if second.Title != "File Map / big.go" {
		t.Fatalf("re-read title = %q, want the intercepted map", second.Title)
	}
	if strings.Contains(second.Output, "return 7\n") {
		t.Fatalf("map-only read leaked content:\n%s", truncate(second.Output))
	}

	// The map must not have moved the bookmark: session-a still sits at the
	// last revision whose content it actually saw.
	if seen := g.log.Seen(key, "session-a"); seen != 0 {
		t.Fatalf("map-only read moved session-a's bookmark to %d, want 0 — a map is not content", seen)
	}

	// So the next edit, whose anchors were chosen against revision-0 content,
	// is still refused...
	res := g.edit("session-a", map[string]any{"path": path, "old_string": "return 7", "new_string": "return 70"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal after a map-only read:\n%s", res.Title, res.Output)
	}
	if got := g.content(path); got != bVersion {
		t.Fatalf("a refused edit still wrote: file = %q, want session-b's content", got)
	}

	// ...and the remedy is the real read: content through a range moves the
	// bookmark onto session-b's revision, and the same edit then applies.
	if _, err := g.call("session-a", ReadTool{}.Execute, map[string]any{
		"path":   path,
		"ranges": []map[string]any{{"start_line": 1, "end_line": 2000}},
	}); err != nil {
		t.Fatalf("re-read after the refusal: %v", err)
	}
	if seen := g.log.Seen(key, "session-a"); seen != 1 {
		t.Fatalf("content re-read left bookmark %d, want 1", seen)
	}
	g.edit("session-a", map[string]any{"path": path, "old_string": "return 7", "new_string": "return 70"})
	if got := g.content(path); !strings.Contains(got, "return 70") {
		t.Fatalf("the retry after a real read did not apply: file = %q", got)
	}
}
