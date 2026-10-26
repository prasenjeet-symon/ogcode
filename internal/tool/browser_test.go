package tool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/browser"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// fakeDispatcher records Dispatch calls and answers them from one canned
// payload.
type fakeDispatcher struct {
	status browser.Status
	calls  []browserCall
	data   map[string]any
	err    error
}

type browserCall struct {
	op   string
	tab  *int64
	args map[string]any
}

func (f *fakeDispatcher) Status() browser.Status { return f.status }

func (f *fakeDispatcher) Dispatch(_ context.Context, op string, tab *int64, args map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, browserCall{op: op, tab: tab, args: args})
	if f.err != nil {
		return nil, f.err
	}
	return f.data, nil
}

func browserTctx() Context {
	return Context{SessionID: session.SessionID("ses_browser_test")}
}

func execBrowser(t *testing.T, tl ToolDef, args map[string]any) (Result, error) {
	t.Helper()
	if len(args) == 0 {
		args = map[string]any{}
	}
	buf, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tl.Execute(context.Background(), buf, browserTctx())
}

func assertUnpaired(t *testing.T, res Result, err error, where string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: err = %v, want a Result-shaped refusal", where, err)
	}
	if !strings.Contains(res.Output, "not connected") {
		t.Errorf("%s: output = %q, want the pairing refusal", where, res.Output)
	}
	if res.Metadata["connected"] != false {
		t.Errorf("%s: metadata connected = %v, want false", where, res.Metadata["connected"])
	}
}

func TestBrowserStatusWithoutADispatcher(t *testing.T) {
	res, err := BrowserStatusTool{Disp: nil}.Execute(context.Background(), json.RawMessage(`{}`), browserTctx())
	assertUnpaired(t, res, err, "nil dispatcher")
}

func TestBrowserNavigateRefusesWhenUnpaired(t *testing.T) {
	fake := &fakeDispatcher{}
	res, err := execBrowser(t, BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://example.test/"})
	assertUnpaired(t, res, err, "unpaired navigate")
	if len(fake.calls) != 0 {
		t.Errorf("unpaired navigate still dispatched: %#v", fake.calls)
	}
}

func TestBrowserNavigateTreatsADroppedBridgeAsUnpaired(t *testing.T) {
	// Status says paired; Dispatch fails with the sentinel — the bridge dropped
	// between the status check and the op. That must yield the pairing refusal
	// (not a raw error) AND one recorded dispatch.
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, err: browser.ErrNotConnected}
	res, err := execBrowser(t, BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://example.test/"})
	assertUnpaired(t, res, err, "dropped bridge")
	if len(fake.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(fake.calls))
	}
}

func TestBrowserStatusReportsTabsWhenPaired(t *testing.T) {
	fake := &fakeDispatcher{
		status: browser.Status{
			Connected: true,
			Version:   "1.2",
			Tabs:      []browser.TabInfo{{ID: 5, URL: "https://example.test/", Title: "Example", Granted: true}},
		},
	}
	res, err := execBrowser(t, BrowserStatusTool{Disp: fake}, nil)
	if err != nil {
		t.Fatalf("paired status: %v", err)
	}
	if res.Metadata["connected"] != true {
		t.Errorf("metadata connected = %v, want true", res.Metadata["connected"])
	}
	if n, ok := res.Metadata["tabs"].(int); !ok || n != 1 {
		t.Errorf("metadata tabs = %#v, want 1", res.Metadata["tabs"])
	}
	if !strings.Contains(res.Output, "example.test") {
		t.Errorf("output = %q, want the granted tab's URL", res.Output)
	}
}

func TestBrowserNavigateRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{"ok": true}}
	res, err := execBrowser(t, BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://example.test/", "newTab": true, "tab": 7})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.op != browser.OpNavigate {
		t.Errorf("op = %q, want %q", c.op, browser.OpNavigate)
	}
	if c.tab == nil || *c.tab != 7 {
		t.Errorf("tab = %v, want 7", c.tab)
	}
	if c.args["url"] != "https://example.test/" || c.args["newTab"] != true {
		t.Errorf("args = %#v, want url + newTab", c.args)
	}
	if _, present := c.args["tab"]; present {
		t.Errorf("args carried the tab selector: %#v", c.args)
	}
	if res.Title != "Browser navigate" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Errorf("output = %q, want the extension payload", res.Output)
	}
}

func TestBrowserNavigateWithoutATabDrivesTheLastGrantedTab(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{
		Connected: true,
		Tabs: []browser.TabInfo{
			{ID: 3, Granted: false},
			{ID: 1, Granted: true},
			{ID: 4, Granted: true, URL: "https://after.test/"},
		},
	}, data: map[string]any{"tabId": 4}}
	res, err := execBrowser(t, BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://after.test/"})
	if err != nil {
		t.Fatalf("navigate without tab: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	if c := fake.calls[0]; c.tab == nil || *c.tab != 4 {
		t.Errorf("tab = %v, want the last granted tab 4", c.tab)
	}
	if res.Title != "Browser navigate" {
		t.Errorf("title = %q", res.Title)
	}
}

func TestBrowserNavigateNewTabNeedsNoTabAtAll(t *testing.T) {
	// Even where a tab is granted, an open must not silently steer it.
	fake := &fakeDispatcher{status: browser.Status{
		Connected: true,
		Tabs:      []browser.TabInfo{{ID: 7, Granted: true}},
	}, data: map[string]any{"tabId": 99}}
	if _, err := execBrowser(t, BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://fresh.test/", "newTab": true}); err != nil {
		t.Fatalf("newTab navigate: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.tab != nil {
		t.Errorf("tab = %v, want nil (the extension opens its own tab)", c.tab)
	}
	if c.args["newTab"] != true {
		t.Errorf("args = %#v, want newTab", c.args)
	}
}

func TestBrowserOpsWithoutAGrantedTabRefuseWithTheRemedy(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{
		Connected: true,
		Tabs: []browser.TabInfo{
			{ID: 1, Granted: false},
			{ID: 2, Granted: false},
		},
	}}
	for _, call := range []struct {
		tool ToolDef
		args map[string]any
	}{
		{BrowserNavigateTool{Disp: fake}, map[string]any{"url": "https://example.test/"}},
		{BrowserSnapshotTool{Disp: fake}, nil},
		{BrowserClickTool{Disp: fake}, map[string]any{"ref": 1}},
	} {
		res, err := execBrowser(t, call.tool, call.args)
		if err != nil {
			t.Fatalf("refusal: %v", err)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("an op dispatched without a granted tab: %#v", fake.calls)
		}
		if !strings.Contains(res.Output, "allow") || !strings.Contains(res.Output, "granted") {
			t.Errorf("output = %q, want the grant remedy", res.Output)
		}
	}
}

func TestBrowserOpsDispatchWhenTheInventoryHasNotArrived(t *testing.T) {
	// No hello yet — Tabs empty. The op passes through with no tab so the
	// extension answers with its own tab error, as it always has.
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserKeyTool{Disp: fake}, map[string]any{"key": "Enter"}); err != nil {
		t.Fatalf("key: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want the dispatch kept when no inventory has arrived", len(fake.calls))
	}
}

func TestBrowserClickRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{}}
	if _, err := execBrowser(t, BrowserClickTool{Disp: fake}, map[string]any{"ref": 3, "tab": 2}); err != nil {
		t.Fatalf("ref click: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpClick {
		t.Fatalf("calls = %#v, want one click", fake.calls)
	}
	c := fake.calls[0]
	if c.tab == nil || *c.tab != 2 {
		t.Errorf("tab = %v, want 2", c.tab)
	}
	if got, ok := c.args["ref"].(float64); !ok || got != 3 {
		t.Errorf("ref arg = %#v, want 3", c.args["ref"])
	}

	selFake := &fakeDispatcher{
		status: browser.Status{
			Connected: true,
			Tabs:      []browser.TabInfo{{ID: 2, Granted: true}},
		},
	}
	if _, err := execBrowser(t, BrowserClickTool{Disp: selFake}, map[string]any{"selector": "#buy-now"}); err != nil {
		t.Fatalf("selector click: %v", err)
	}
	if len(selFake.calls) != 1 || selFake.calls[0].args["selector"] != "#buy-now" {
		t.Errorf("selector click args = %#v", selFake.calls)
	}
	if c := selFake.calls[0]; c.tab == nil || *c.tab != 2 {
		t.Errorf("tab = %v, want the last granted tab 2 by default", c.tab)
	}
}

func TestBrowserTypeRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserTypeTool{Disp: fake}, map[string]any{"ref": 2, "text": "hi there", "submit": true}); err != nil {
		t.Fatalf("type: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpType {
		t.Fatalf("calls = %#v, want one type", fake.calls)
	}
	args := fake.calls[0].args
	if args["text"] != "hi there" || args["submit"] != true {
		t.Errorf("type args = %#v", args)
	}
	if _, present := args["clear"]; present {
		t.Errorf("unset clear must not be sent: %#v", args)
	}
}

func TestBrowserKeyRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserKeyTool{Disp: fake}, map[string]any{"key": "Enter"}); err != nil {
		t.Fatalf("key: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpKey || fake.calls[0].args["key"] != "Enter" {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func TestBrowserScrollRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserScrollTool{Disp: fake}, map[string]any{"direction": "Down", "amount": 400, "tab": 9}); err != nil {
		t.Fatalf("scroll: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpScroll {
		t.Fatalf("calls = %#v", fake.calls)
	}
	c := fake.calls[0]
	if c.args["direction"] != "down" {
		t.Errorf("direction = %#v, want lowercase down", c.args["direction"])
	}
	if c.args["amount"] != float64(400) {
		t.Errorf("amount = %#v, want 400", c.args["amount"])
	}
	if c.tab == nil || *c.tab != 9 {
		t.Errorf("tab = %v, want 9", c.tab)
	}
}

func TestBrowserNetworkRefusesWhenUnpaired(t *testing.T) {
	fake := &fakeDispatcher{}
	res, err := execBrowser(t, BrowserNetworkTool{Disp: fake}, map[string]any{"url": "example.test"})
	assertUnpaired(t, res, err, "unpaired network")
	if len(fake.calls) != 0 {
		t.Errorf("unpaired network still dispatched: %#v", fake.calls)
	}
}

func TestBrowserConsoleRefusesWhenUnpaired(t *testing.T) {
	fake := &fakeDispatcher{}
	res, err := execBrowser(t, BrowserConsoleTool{Disp: fake}, map[string]any{"level": "error"})
	assertUnpaired(t, res, err, "unpaired console")
	if len(fake.calls) != 0 {
		t.Errorf("unpaired console still dispatched: %#v", fake.calls)
	}
}

func TestBrowserNetworkRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{"watching": true}}
	if _, err := execBrowser(t, BrowserNetworkTool{Disp: fake}, map[string]any{"url": "api.example", "max": 50, "tab": 4}); err != nil {
		t.Fatalf("network: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpNetwork {
		t.Fatalf("calls = %#v, want one network op", fake.calls)
	}
	c := fake.calls[0]
	if c.tab == nil || *c.tab != 4 {
		t.Errorf("tab = %v, want the selected tab 4", c.tab)
	}
	if c.args["url"] != "api.example" {
		t.Errorf("url arg = %#v, want api.example", c.args["url"])
	}
	if c.args["max"] != float64(50) {
		t.Errorf("max arg = %#v, want 50", c.args["max"])
	}
	if _, present := c.args["requestId"]; present {
		t.Errorf("unset requestId must not be sent: %#v", c.args)
	}
}

func TestBrowserNetworkFetchesABodyByRequestID(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{
		"watching": true, "entry": map[string]any{"id": "req.42"}, "body": "%PDF",
	}}
	if _, err := execBrowser(t, BrowserNetworkTool{Disp: fake}, map[string]any{"requestId": "req.42"}); err != nil {
		t.Fatalf("network body: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpNetwork {
		t.Fatalf("calls = %#v, want one network op", fake.calls)
	}
	c := fake.calls[0]
	if c.args["requestId"] != "req.42" {
		t.Errorf("requestId arg = %#v, want req.42", c.args["requestId"])
	}
	if _, present := c.args["url"]; present {
		t.Errorf("a body fetch must not carry the url filter: %#v", c.args)
	}
}

func TestBrowserConsoleRoundTrip(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{"watching": true}}
	if _, err := execBrowser(t, BrowserConsoleTool{Disp: fake}, map[string]any{"level": "error", "source": "exception", "max": 25}); err != nil {
		t.Fatalf("console: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpConsole {
		t.Fatalf("calls = %#v, want one console op", fake.calls)
	}
	args := fake.calls[0].args
	if args["level"] != "error" || args["source"] != "exception" {
		t.Errorf("console args = %#v, want level and source", args)
	}
	if args["max"] != float64(25) {
		t.Errorf("max arg = %#v, want 25", args["max"])
	}
}

func TestBrowserReadToolsShapeMalformedArgs(t *testing.T) {
	// Wrong-typed args fail the decode and come back in the same Result
	// shape the other browser tools use — not as a Go error.
	for _, call := range []struct {
		tool  ToolDef
		title string
		args  string
	}{
		{BrowserNetworkTool{Disp: &fakeDispatcher{}}, "Browser network", `{"max":"lots"}`},
		{BrowserConsoleTool{Disp: &fakeDispatcher{}}, "Browser console", `{"level":42}`},
	} {
		res, err := call.tool.Execute(context.Background(), json.RawMessage(call.args), browserTctx())
		if err != nil {
			t.Fatalf("%s: err = %v, want a Result-shaped decode failure", call.title, err)
		}
		if res.Title != call.title {
			t.Errorf("%s: title = %q", call.title, res.Title)
		}
		if !strings.Contains(res.Output, ": bad arguments:") {
			t.Errorf("%s: output = %q, want the bad-arguments shape", call.title, res.Output)
		}
	}
}

func TestBrowserScreenshotNeedsVision(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	tctx := browserTctx()
	tctx.ModelSupportsImages = false
	res, err := BrowserScreenshotTool{Disp: fake}.Execute(context.Background(), json.RawMessage(`{}`), tctx)
	if err != nil {
		t.Fatalf("no-vision screenshot: %v", err)
	}
	if !strings.Contains(res.Output, "cannot view images") {
		t.Errorf("output = %q, want the vision refusal", res.Output)
	}
	if res.Image != nil {
		t.Error("no-vision screenshot attached an image")
	}
	if len(fake.calls) != 0 {
		t.Errorf("no-vision screenshot dispatched: %#v", fake.calls)
	}
}

func TestBrowserScreenshotAttachesImage(t *testing.T) {
	fake := &fakeDispatcher{
		status: browser.Status{Connected: true},
		data:   map[string]any{"base64": "AAAA", "url": "https://example.test/"},
	}
	tctx := browserTctx()
	tctx.ModelSupportsImages = true
	res, err := BrowserScreenshotTool{Disp: fake}.Execute(context.Background(), json.RawMessage(`{}`), tctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	want := "Screenshot captured of https://example.test/; the image is attached."
	if res.Output != want {
		t.Errorf("output = %q, want %q", res.Output, want)
	}
	if res.Image == nil {
		t.Fatal("screenshot attached no image")
	}
	if res.Image.MediaType != "image/png" || res.Image.Data != "AAAA" {
		t.Errorf("image = %#v, want png AAAA", res.Image)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpScreenshot {
		t.Fatalf("calls = %#v, want one screenshot", fake.calls)
	}
	if fake.calls[0].args["format"] != "png" {
		t.Errorf("screenshot args = %#v, want format png", fake.calls[0].args)
	}
}

// Newer extensions re-encode the capture to JPEG and name the media type in
// the response; the tool must label the attached image with what came back.
func TestBrowserScreenshotHonorsReturnedImageFormat(t *testing.T) {
	fake := &fakeDispatcher{
		status: browser.Status{Connected: true},
		data:   map[string]any{"base64": "BBBB", "url": "https://example.test/", "format": "image/jpeg"},
	}
	tctx := browserTctx()
	tctx.ModelSupportsImages = true
	res, err := BrowserScreenshotTool{Disp: fake}.Execute(context.Background(), json.RawMessage(`{}`), tctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if res.Image == nil || res.Image.MediaType != "image/jpeg" || res.Image.Data != "BBBB" {
		t.Errorf("image = %#v, want jpeg BBBB", res.Image)
	}
}

// Every browser tool answers a dispatcher it does not have with the pairing
// refusal, exactly the way browser_status already did — none of them panics
// on the missing bridge. Each case sends args that clear the tool's own
// validator, so the nil-dispatcher guard is the only thing standing between
// the call and the bridge.
func TestBrowserToolsRefuseWithoutADispatcher(t *testing.T) {
	visionTctx := browserTctx()
	visionTctx.ModelSupportsImages = true
	cases := []struct {
		name string
		tl   ToolDef
		args map[string]any
		tctx Context
	}{
		{"browser_status", BrowserStatusTool{}, nil, browserTctx()},
		{"browser_navigate", BrowserNavigateTool{}, map[string]any{"url": "https://example.test/"}, browserTctx()},
		{"browser_snapshot", BrowserSnapshotTool{}, nil, browserTctx()},
		{"browser_content", BrowserContentTool{}, nil, browserTctx()},
		{"browser_click", BrowserClickTool{}, map[string]any{"ref": 3}, browserTctx()},
		{"browser_type", BrowserTypeTool{}, map[string]any{"ref": 2, "text": "hello"}, browserTctx()},
		{"browser_key", BrowserKeyTool{}, map[string]any{"key": "Enter"}, browserTctx()},
		{"browser_scroll", BrowserScrollTool{}, map[string]any{"direction": "down"}, browserTctx()},
		{"browser_screenshot", BrowserScreenshotTool{}, map[string]any{}, visionTctx},
		{"browser_network", BrowserNetworkTool{}, nil, browserTctx()},
		{"browser_console", BrowserConsoleTool{}, nil, browserTctx()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			res, err := tc.tl.Execute(context.Background(), buf, tc.tctx)
			assertUnpaired(t, res, err, tc.name)
		})
	}
}

// A dispatch that fails outright (a bounced op, a timeout) rides through
// untouched: the error text is the output, no image is attached, and the
// result carries the error tag run stamps on the failed dispatch. The tool
// must decide this from the tag, not by sniffing its own output for a
// reworded "timed out".
func TestBrowserScreenshotPassesThroughADispatchError(t *testing.T) {
	fake := &fakeDispatcher{
		status: browser.Status{Connected: true},
		err:    errors.New("take screenshot: op timed out after 30s"),
	}
	tctx := browserTctx()
	tctx.ModelSupportsImages = true
	res, err := BrowserScreenshotTool{Disp: fake}.Execute(context.Background(), json.RawMessage(`{}`), tctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Errorf("output = %q, want the dispatch error verbatim", res.Output)
	}
	if res.Image != nil {
		t.Error("a failed screenshot attached an image")
	}
	if _, ok := res.Metadata["error"]; !ok {
		t.Errorf("metadata = %#v, want the error tag run sets on a failed dispatch", res.Metadata)
	}
	if len(fake.calls) != 1 {
		t.Errorf("calls = %#v, want one screenshot", fake.calls)
	}
}

// A bridge that drops mid-flight turns the already-running op into the
// pairing refusal — the same shape an unpaired start answers — rather than a
// bare error, and the tool passes it on without rewrapping it.
func TestBrowserScreenshotTreatsADroppedBridgeAsUnpaired(t *testing.T) {
	fake := &fakeDispatcher{
		status: browser.Status{Connected: true},
		err:    browser.ErrNotConnected,
	}
	tctx := browserTctx()
	tctx.ModelSupportsImages = true
	res, err := BrowserScreenshotTool{Disp: fake}.Execute(context.Background(), json.RawMessage(`{}`), tctx)
	assertUnpaired(t, res, err, "dropped bridge")
}

func TestNewBrowserToolsElevenUniqueIDs(t *testing.T) {
	tools := NewBrowserTools(nil)
	want := map[string]bool{
		"browser_status":     true,
		"browser_navigate":   true,
		"browser_snapshot":   true,
		"browser_content":    true,
		"browser_click":      true,
		"browser_type":       true,
		"browser_key":        true,
		"browser_scroll":     true,
		"browser_screenshot": true,
		"browser_network":    true,
		"browser_console":    true,
	}
	if len(tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(tools), len(want))
	}
	for _, tl := range tools {
		if !want[tl.ID()] {
			t.Errorf("unexpected browser tool id %q", tl.ID())
			continue
		}
		delete(want, tl.ID())
	}
	if len(want) != 0 {
		t.Errorf("missing tool ids: %v", want)
	}
}

// TestBrowserToolIDsMatchesRegistration pins BrowserToolIDs against the list
// NewBrowserTools actually builds: the lazy turn toolset offers these ids by
// name, so a registration order that drifts from the reported ids would put
// the wrong family on the wire.
func TestBrowserToolIDsMatchesRegistration(t *testing.T) {
	want := make([]string, 0, 11)
	for _, tl := range NewBrowserTools(nil) {
		want = append(want, tl.ID())
	}
	got := BrowserToolIDs()
	if !slices.Equal(got, want) {
		t.Errorf("BrowserToolIDs() = %v, want registration order %v", got, want)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("BrowserToolIDs() repeats %q", id)
		}
		seen[id] = true
	}
	if len(got) != 11 {
		t.Errorf("got %d ids, want 11", len(got))
	}
}

// TestBrowserSnapshotRendersCompactLines pins the compact one-line-per-element
// snapshot view: a header line carrying url/title/viewport, then
// `[ref] tag "text"` plus non-empty extras. The selector plumbing, the raw
// tag name, empty optional fields and an unchecked flag never render; the
// truncation flag gets its own short line.
func TestBrowserSnapshotRendersCompactLines(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{
		"url":       "https://shop.example.test/",
		"title":     "Checkout",
		"viewport":  map[string]any{"w": 1280.0, "h": 800.0},
		"truncated": true,
		"elements": []any{
			map[string]any{"ref": 1.0, "tag": "a", "tn": "a", "text": "Sign in", "selector": "#login", "href": "https://accounts.example.test/login?next=/cart"},
			map[string]any{"ref": 2.0, "tag": "textbox", "tn": "input", "text": "Email address", "selector": "input#email", "placeholder": "you@example.test"},
			map[string]any{"ref": 3.0, "tag": "input", "tn": "input", "text": "", "selector": "input#q", "value": "hello"},
			map[string]any{"ref": 4.0, "tag": "checkbox", "tn": "input", "text": "Remember me", "checked": false},
			map[string]any{"ref": 5.0, "tag": "checkbox", "tn": "input", "text": "Remember me", "checked": true},
			map[string]any{"ref": 6.0, "tag": "button", "tn": "button", "text": "Continue"},
		},
	}}
	res, err := execBrowser(t, BrowserSnapshotTool{Disp: fake}, nil)
	if err != nil {
		t.Fatalf("compact snapshot: %v", err)
	}
	for _, wantLine := range []string{
		"url https://shop.example.test/ | title Checkout | viewport 1280x800",
		`[1] a "Sign in" https://accounts.example.test/login?next=/cart`,
		`[2] textbox "Email address" placeholder "you@example.test"`,
		`[3] input "" value "hello"`,
		`[5] checkbox "Remember me" checked`,
		`[6] button "Continue"`,
		"(truncated)",
	} {
		if !strings.Contains(res.Output, wantLine) {
			t.Errorf("output does not carry %q:\n%s", wantLine, res.Output)
		}
	}
	// The unchecked box renders bare: the false flag is an empty extra.
	if got := strings.Contains(res.Output, "[4]") && strings.Contains(res.Output, "false"); got {
		t.Errorf("output carries the unchecked flag:\n%s", res.Output)
	}
	// Plumbing the model never needs: selector values and the raw tag name.
	for _, absent := range []string{"#login", "input#email", "tn"} {
		if strings.Contains(res.Output, absent) {
			t.Errorf("output leaks %q:\n%s", absent, res.Output)
		}
	}
}

// TestBrowserSnapshotForwardsQueryAndLimit pins the optional snapshot filters
// they hit the wire verbatim when given and are absent entirely when not —
// the filtering itself stays on the extension side.
func TestBrowserSnapshotForwardsQueryAndLimit(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserSnapshotTool{Disp: fake}, map[string]any{"query": "sign in", "limit": 20}); err != nil {
		t.Fatalf("filtered snapshot: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].op != browser.OpSnapshot {
		t.Fatalf("calls = %#v, want one snapshot", fake.calls)
	}
	args := fake.calls[0].args
	if args["query"] != "sign in" {
		t.Errorf("query arg = %#v, want it forwarded verbatim", args["query"])
	}
	if args["limit"] != float64(20) {
		t.Errorf("limit arg = %#v, want it forwarded verbatim", args["limit"])
	}

	plain := &fakeDispatcher{status: browser.Status{Connected: true}}
	if _, err := execBrowser(t, BrowserSnapshotTool{Disp: plain}, nil); err != nil {
		t.Fatalf("plain snapshot: %v", err)
	}
	if len(plain.calls) != 1 {
		t.Fatalf("plain snapshot calls = %d, want 1", len(plain.calls))
	}
	for _, key := range []string{"query", "limit"} {
		if _, present := plain.calls[0].args[key]; present {
			t.Errorf("unset %s must not be sent: %#v", key, plain.calls[0].args)
		}
	}
}

// TestBrowserSnapshotHeaderShowsTotalMatchingWhenPresent pins the optional
// totalMatching passthrough: the header reports `showing N of M` when the
// extension actually filtered (M > N) and renders as today when the field
// is absent.
func TestBrowserSnapshotHeaderShowsTotalMatchingWhenPresent(t *testing.T) {
	elements := []any{
		map[string]any{"ref": 1.0, "tag": "a", "tn": "a", "text": "One"},
		map[string]any{"ref": 2.0, "tag": "button", "tn": "button", "text": "Two"},
	}
	filtered := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{
		"url": "https://example.test/", "title": "Find", "viewport": map[string]any{"w": 1024.0, "h": 768.0},
		"totalMatching": 137.0, "elements": elements,
	}}
	res, err := execBrowser(t, BrowserSnapshotTool{Disp: filtered}, map[string]any{"query": "e"})
	if err != nil {
		t.Fatalf("filtered snapshot: %v", err)
	}
	if !strings.Contains(res.Output, "showing 2 of 137") {
		t.Errorf("output = %q, want the matching count in the header", res.Output)
	}

	unfiltered := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{
		"url": "https://example.test/", "title": "Find", "viewport": map[string]any{"w": 1024.0, "h": 768.0},
		"elements": elements,
	}}
	res, err = execBrowser(t, BrowserSnapshotTool{Disp: unfiltered}, nil)
	if err != nil {
		t.Fatalf("unfiltered snapshot: %v", err)
	}
	if strings.Contains(res.Output, "showing") {
		t.Errorf("output = %q, want no showing clause when totalMatching is absent", res.Output)
	}
	if !strings.Contains(res.Output, "viewport 1024x768") {
		t.Errorf("output = %q, want the header as today when totalMatching is absent", res.Output)
	}
}

// TestBrowserSnapshotFallsBackToJSONWhenPayloadIsNotASnapshot pins the guard
// beside the new rendering: a payload without an elements array — an error
// body, an older extension — still comes through as the raw JSON it always
// rendered.
func TestBrowserSnapshotFallsBackToJSONWhenPayloadIsNotASnapshot(t *testing.T) {
	fake := &fakeDispatcher{status: browser.Status{Connected: true}, data: map[string]any{
		"ok": true, "detail": "extension error",
	}}
	res, err := execBrowser(t, BrowserSnapshotTool{Disp: fake}, nil)
	if err != nil {
		t.Fatalf("non-snapshot payload: %v", err)
	}
	if !strings.Contains(res.Output, "extension error") || !strings.Contains(res.Output, "{\n") {
		t.Errorf("output = %q, want the plain JSON fallback", res.Output)
	}
}
