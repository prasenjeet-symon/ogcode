package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/browser"
)

// BrowserDispatcher is the live bridge behind every browser_* tool. A nil
// dispatcher or an unpaired bridge turns each call into a paired-up refusal
// rather than an error.
type BrowserDispatcher = browser.Dispatcher

// browserPairHint is the tail every unpaired browser call carries: the skill
// is the setup manual, the endpoint is the one thing this side controls.
const browserPairHint = "To pair it: fetch GET /api/browser/pair on this ogcode server for the ws:// pairing URL and token, load the ogcode companion extension (browser-extension/ in the ogcode repository) unpacked into Chrome, and paste the origin and token into its side panel. The browser-takeover skill walks through the whole setup."

// unpaired is the refusal shape every browser tool answers when no extension
// is connected. It is a Result, not an error, so the transcript keeps a
// readable instruction instead of a stack trace.
func unpaired(status browser.Status) Result {
	return Result{
		Title: "Browser",
		Output: "The ogcode browser extension is not connected to this server, so the browser cannot be driven yet. " +
			browserPairHint,
		Metadata: map[string]any{"connected": false},
	}
}

// browserBase carries what the eleven tools share: the dispatcher and the
// args decode.
type browserBase struct {
	disp BrowserDispatcher
}

// status reads the bridge's state. A nil dispatcher reads as an unpaired
// bridge — zero state, nothing connected — so every tool that talks to it
// refuses through the shared not-connected check instead of panicking on the
// missing bridge.
func (b browserBase) status() browser.Status {
	if b.disp == nil {
		return browser.Status{}
	}
	return b.disp.Status()
}

// run decodes args, calls the bridge and returns its payload. A call that
// omitted tab drives the last granted tab; a navigate with newTab
// deliberately keeps tab nil — the extension opens its own tab and grants
// it. An optional render func turns the extension payload into a friendlier
// model view; when it declines (ok=false) the payload falls back to pretty
// JSON, so unexpected shapes still come through verbatim.
func (b browserBase) run(ctx context.Context, title, op string, in any, tab *int64, render ...func(map[string]any) (string, bool)) (Result, error) {
	st := b.status()
	if !st.Connected {
		return unpaired(st), nil
	}

	args := map[string]any{}
	if data, err := json.Marshal(in); err == nil && len(data) > 0 {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err == nil {
			args = m
		}
	}
	delete(args, "tab")

	if tab == nil {
		newTab := false
		if on, ok := args["newTab"].(bool); ok {
			newTab = on
		}
		if !(op == browser.OpNavigate && newTab) {
			// Fill the optional-tab default the schemas document: the last
			// granted tab in the extension's announced inventory.
			tab = lastGrantedTab(st)
			if tab == nil && len(st.Tabs) > 0 {
				// The inventory is known and nothing on it is granted — refuse
				// with the remedy rather than dispatch an op the extension
				// will bounce.
				return Result{
					Title:    title,
					Output:   "No tab was granted to ogcode in the extension side panel, so this op has no tab to act on. Tick allow on a tab there, pass tab:<id> from browser_status, or use browser_navigate with newTab to open a tab that is granted automatically.",
					Metadata: map[string]any{"connected": true, "tabs": len(st.Tabs), "granted": 0},
				}, nil
			}
		}
	}

	data, err := b.disp.Dispatch(ctx, op, tab, args)
	if err != nil {
		if err == browser.ErrNotConnected {
			return unpaired(st), nil
		}
		// Tag a failed dispatch so a caller can tell it from a success it
		// cannot use — the screenshot tool reads this key rather than sniffing
		// its own output for one.
		return Result{Title: title, Output: err.Error(), Metadata: map[string]any{"error": true}}, nil
	}
	result := Result{Title: title, Metadata: args}
	if len(render) > 0 && render[0] != nil {
		if out, ok := render[0](data); ok {
			result.Output = out
			return result, nil
		}
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		out = []byte(fmt.Sprintf("%v", data))
	}
	result.Output = string(out)
	return result, nil
}

// lastGrantedTab implements the optional-tab default the tool schemas
// document: the last granted tab in the extension's hello inventory. It
// returns nil when the inventory grants nothing (all tabs unticked, or no
// hello yet) — the caller refuses or passes the nil through untouched.
func lastGrantedTab(st browser.Status) *int64 {
	var last *int64
	for i := range st.Tabs {
		if st.Tabs[i].Granted {
			id := st.Tabs[i].ID
			last = &id
		}
	}
	return last
}

// tabSchema is the optional tab selector shared by every op's schema: without
// it the bridge drives the last granted tab in the extension's inventory.
const tabSchema = `"tab":{"type":"integer","description":"Optional tab id to act on, from browser_status or the latest snapshot. Defaults to the last granted tab in the extension's inventory; a browser_navigate with newTab set needs no tab."}`

// browserParams builds a Parameters schema around the given properties.
func browserParams(props string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{%s}}`, props))
}

// BrowserStatusTool reports the takeover connection. It answers even when the
// extension is not paired — that is exactly when it is useful.
type BrowserStatusTool struct{ Disp BrowserDispatcher }

func (BrowserStatusTool) ID() string { return "browser_status" }

func (BrowserStatusTool) Description() string {
	return "Report the browser takeover connection: whether the ogcode companion extension is paired to this server, its version, and which tabs the user has granted for takeover (with their URLs and titles). Always available, even before pairing — call it first to check the state of the bridge."
}

func (BrowserStatusTool) Parameters() json.RawMessage {
	return browserParams("")
}

func (t BrowserStatusTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	if t.Disp == nil {
		return unpaired(browser.Status{}), nil
	}
	st := t.Disp.Status()
	if !st.Connected {
		return unpaired(st), nil
	}
	data, _ := json.MarshalIndent(st, "", "  ")
	return Result{
		Title:    "Browser status",
		Output:   string(data),
		Metadata: map[string]any{"connected": true, "tabs": len(st.Tabs)},
	}, nil
}

// BrowserNavigateTool moves a granted tab to a URL.
type BrowserNavigateTool struct{ Disp BrowserDispatcher }

func (BrowserNavigateTool) ID() string { return "browser_navigate" }

func (BrowserNavigateTool) Description() string {
	return "Navigate a granted browser tab to a URL and wait for the load, or set newTab to open the URL in a brand-new tab instead: the extension grants that new tab automatically and returns its tab id, so snapshot it next and act there. Re-snapshot after navigating: element refs from the previous page are stale."
}

func (BrowserNavigateTool) Parameters() json.RawMessage {
	return browserParams(`"url":{"type":"string","description":"The URL to open in the granted tab."},"newTab":{"type":"boolean","description":"Open a brand-new tab for this URL instead of steering the granted one; the new tab is granted automatically and its id rides the result."},` + tabSchema)
}

func (t BrowserNavigateTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		URL    string `json:"url"`
		NewTab bool   `json:"newTab"`
		Tab    *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_navigate", err), nil
	}
	if strings.TrimSpace(in.URL) == "" {
		return Result{Title: "Browser navigate", Output: "url is required."}, nil
	}
	return browserBase{t.Disp}.run(ctx, "Browser navigate", browser.OpNavigate, map[string]any{"url": in.URL, "newTab": in.NewTab}, in.Tab)
}

// BrowserSnapshotTool returns the compact numbered interactive-element view.
type BrowserSnapshotTool struct{ Disp BrowserDispatcher }

func (BrowserSnapshotTool) ID() string { return "browser_snapshot" }

func (BrowserSnapshotTool) Description() string {
	return "Read a granted browser tab as a compact list of numbered interactive elements (links, buttons, inputs, selects, textareas) with refs you act on. Always snapshot before clicking or typing: refs belong to this snapshot and go stale the moment the page changes. Element text is capped, so the whole snapshot stays small; pass query to narrow the elements or limit to cap their count."
}

func (BrowserSnapshotTool) Parameters() json.RawMessage {
	return browserParams(tabSchema + `,` +
		`"query":{"type":"string","description":"Optional filter the extension applies: only elements whose text or value contains this string are reported."},` +
		`"limit":{"type":"integer","description":"Optional positive cap on how many elements the extension reports."}`)
}

func (t BrowserSnapshotTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Tab   *int64 `json:"tab"`
		Query string `json:"query"`
		Limit *int   `json:"limit"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_snapshot", err), nil
	}
	callArgs := map[string]any{}
	if in.Query != "" {
		callArgs["query"] = in.Query
	}
	if in.Limit != nil {
		callArgs["limit"] = *in.Limit
	}
	return browserBase{t.Disp}.run(ctx, "Browser snapshot", browser.OpSnapshot, callArgs, in.Tab, renderSnapshotResult)
}

// renderSnapshotResult turns the extension's snapshot payload into the
// compact model view: a header line carrying url/title/viewport, then one
// line per element as [<ref>] <tag> "<text>" followed by its non-empty
// extras (the href bare, value and placeholder quoted, checked when set).
// The selector plumbing, the raw tag name and empty/optional fields never
// render. The refs themselves are passed through untouched, so click/type
// resolve them exactly as before, and any ref bookkeeping stays on the
// extension side where it already lives. An optional totalMatching rides
// the header as "showing N of M" only when the extension actually filtered
// (M > N); a truncated flag gets its own short line. Returns ok=false when
// the payload is not a recognizable snapshot, so run falls back to pretty
// JSON verbatim.
func renderSnapshotResult(data map[string]any) (string, bool) {
	els, ok := data["elements"].([]any)
	if !ok {
		return "", false
	}
	var header []string
	if u := mapStr(data, "url"); u != "" {
		header = append(header, "url "+u)
	}
	if title := mapStr(data, "title"); title != "" {
		header = append(header, "title "+title)
	}
	if vp := snapshotViewport(data["viewport"]); vp != "" {
		header = append(header, "viewport "+vp)
	}
	if total := snapshotInt(data["totalMatching"]); total > len(els) {
		header = append(header, fmt.Sprintf("showing %d of %d", len(els), total))
	}
	var b strings.Builder
	b.WriteString(strings.Join(header, " | ") + "\n")
	for _, e := range els {
		em, ok := e.(map[string]any)
		if !ok {
			return "", false
		}
		line := fmt.Sprintf("[%v] %v \"%v\"", em["ref"], em["tag"], em["text"])
		if href := mapStr(em, "href"); href != "" {
			line += " " + href
		}
		if v := mapStr(em, "value"); v != "" {
			line += " value \"" + v + "\""
		}
		if p := mapStr(em, "placeholder"); p != "" {
			line += " placeholder \"" + p + "\""
		}
		if on, ok := em["checked"].(bool); ok && on {
			line += " checked"
		}
		if dim, ok := em["disabled"].(bool); ok && dim {
			line += " disabled"
		}
		b.WriteString(line + "\n")
	}
	if trunc, ok := data["truncated"].(bool); ok && trunc {
		b.WriteString("(truncated)\n")
	}
	return b.String(), true
}

// mapStr reads a string payload field: anything else (nil, number, object)
// reads as empty and is omitted from the rendering.
func mapStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// snapshotInt reads a JSON number as an int; anything else reads as 0.
func snapshotInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// snapshotViewport renders the snapshot's viewport, which the extension
// sends as {w,h} (rendered WxH); a plain string passes through verbatim.
func snapshotViewport(v any) string {
	switch t := v.(type) {
	case map[string]any:
		w, h := snapshotInt(t["w"]), snapshotInt(t["h"])
		if w > 0 || h > 0 {
			return fmt.Sprintf("%dx%d", w, h)
		}
		return ""
	case string:
		return t
	}
	return ""
}

// BrowserContentTool reads the page's visible text.
type BrowserContentTool struct{ Disp BrowserDispatcher }

func (BrowserContentTool) ID() string { return "browser_content" }

func (BrowserContentTool) Description() string {
	return "Read a granted browser tab's visible text (body innerText, capped). Use it to read prose the snapshot's element texts do not carry; use browser_snapshot when you need to interact instead."
}

func (BrowserContentTool) Parameters() json.RawMessage {
	return browserParams(tabSchema)
}

func (t BrowserContentTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Tab *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_content", err), nil
	}
	return browserBase{t.Disp}.run(ctx, "Browser content", browser.OpReadText, map[string]any{}, in.Tab)
}

// BrowserClickTool clicks a snapshot ref or a CSS selector.
type BrowserClickTool struct{ Disp BrowserDispatcher }

func (BrowserClickTool) ID() string { return "browser_click" }

func (BrowserClickTool) Description() string {
	return "Click an element in a granted browser tab by its ref from the latest browser_snapshot (or a CSS selector). The click is delivered as real input events, so hover menus and focus work. Before a submit, checkout, delete, or anything that sends data — ask the user first."
}

func (BrowserClickTool) Parameters() json.RawMessage {
	return browserParams(`"ref":{"type":"integer","description":"The element's ref number from the latest browser_snapshot."},"selector":{"type":"string","description":"Fallback CSS selector to click when no ref fits."},` + tabSchema)
}

func (t BrowserClickTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Ref      *int   `json:"ref"`
		Selector string `json:"selector"`
		Tab      *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_click", err), nil
	}
	if in.Ref == nil && strings.TrimSpace(in.Selector) == "" {
		return Result{Title: "Browser click", Output: "Give ref (from the latest browser_snapshot) or selector."}, nil
	}
	callArgs := map[string]any{}
	if in.Ref != nil {
		callArgs["ref"] = *in.Ref
	}
	if in.Selector != "" {
		callArgs["selector"] = in.Selector
	}
	return browserBase{t.Disp}.run(ctx, "Browser click", browser.OpClick, callArgs, in.Tab)
}

// BrowserTypeTool fills an input and optionally submits.
type BrowserTypeTool struct{ Disp BrowserDispatcher }

func (BrowserTypeTool) ID() string { return "browser_type" }

func (BrowserTypeTool) Description() string {
	return "Type text into an input, select or contenteditable in a granted browser tab, by ref from the latest browser_snapshot. Clears the field first unless clear is false; with submit true it presses Enter after the text, which submits most forms."
}

func (BrowserTypeTool) Parameters() json.RawMessage {
	return browserParams(`"ref":{"type":"integer","description":"The element's ref number from the latest browser_snapshot."},"text":{"type":"string","description":"The text to type."},"submit":{"type":"boolean","description":"Press Enter after typing (submits most forms)."},"clear":{"type":"boolean","description":"Set false to append instead of replacing the value. Defaults to clearing."},` + tabSchema)
}

func (t BrowserTypeTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Ref    *int   `json:"ref"`
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
		Clear  *bool  `json:"clear"`
		Tab    *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_type", err), nil
	}
	if in.Ref == nil {
		return Result{Title: "Browser type", Output: "ref (from the latest browser_snapshot) is required."}, nil
	}
	callArgs := map[string]any{"ref": *in.Ref, "text": in.Text, "submit": in.Submit}
	if in.Clear != nil {
		callArgs["clear"] = *in.Clear
	}
	return browserBase{t.Disp}.run(ctx, "Browser type", browser.OpType, callArgs, in.Tab)
}

// BrowserKeyTool presses a named key.
type BrowserKeyTool struct{ Disp BrowserDispatcher }

func (BrowserKeyTool) ID() string { return "browser_key" }

func (BrowserKeyTool) Description() string {
	return "Press a named key (Enter, Tab, Escape, ArrowDown, ArrowUp, Backspace, …) into a granted browser tab. For typing text into a field use browser_type instead; keys are for navigation, dismissal and submission."
}

func (BrowserKeyTool) Parameters() json.RawMessage {
	return browserParams(`"key":{"type":"string","description":"The key to press, e.g. Enter, Tab, Escape."},` + tabSchema)
}

func (t BrowserKeyTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Key string `json:"key"`
		Tab *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_key", err), nil
	}
	if strings.TrimSpace(in.Key) == "" {
		return Result{Title: "Browser key", Output: "key is required."}, nil
	}
	return browserBase{t.Disp}.run(ctx, "Browser key", browser.OpKey, map[string]any{"key": in.Key}, in.Tab)
}

// BrowserScrollTool scrolls a granted tab.
type BrowserScrollTool struct{ Disp BrowserDispatcher }

func (BrowserScrollTool) ID() string { return "browser_scroll" }

func (BrowserScrollTool) Description() string {
	return "Scroll a granted browser tab up or down with a real wheel event, so lazy-loaded content loads. amount is pixels; default is most of a viewport."
}

func (BrowserScrollTool) Parameters() json.RawMessage {
	return browserParams(`"direction":{"type":"string","description":"up or down."},"amount":{"type":"integer","description":"Pixels to scroll; defaults to a viewport's worth."},` + tabSchema)
}

func (t BrowserScrollTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Direction string `json:"direction"`
		Amount    int    `json:"amount"`
		Tab       *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_scroll", err), nil
	}
	dir := strings.ToLower(in.Direction)
	if dir != "up" && dir != "down" {
		return Result{Title: "Browser scroll", Output: "direction must be up or down."}, nil
	}
	callArgs := map[string]any{"direction": dir}
	if in.Amount > 0 {
		callArgs["amount"] = in.Amount
	}
	return browserBase{t.Disp}.run(ctx, "Browser scroll", browser.OpScroll, callArgs, in.Tab)
}

// BrowserScreenshotTool captures a PNG and hands it to the model as an image.
type BrowserScreenshotTool struct{ Disp BrowserDispatcher }

func (BrowserScreenshotTool) ID() string { return "browser_screenshot" }

func (BrowserScreenshotTool) Description() string {
	return "Screenshot a granted browser tab and attach the capture as an image (JPEG, long edge capped at 1024 px) for you to look at. Needs a vision-capable model. Use it to verify rendered output — layouts, canvas contents, pixel colors — that text snapshots cannot carry."
}

func (BrowserScreenshotTool) Parameters() json.RawMessage {
	return browserParams(tabSchema)
}

func (t BrowserScreenshotTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Tab *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_screenshot", err), nil
	}
	st := browserBase{t.Disp}.status()
	if !st.Connected {
		return unpaired(st), nil
	}
	if !tctx.ModelSupportsImages {
		return Result{
			Title:  "Browser screenshot",
			Output: "The session's model cannot view images, so the screenshot was not taken. Verify what you need with browser_snapshot or browser_content instead.",
		}, nil
	}

	res, err := browserBase{t.Disp}.run(ctx, "Browser screenshot", browser.OpScreenshot, map[string]any{"format": "png"}, in.Tab)
	// A capture that never came back is passed through as it stands: the
	// pairing refusal and a failed dispatch already carry everything the
	// caller needs, and this tool must not rewrap either. run itself sets both
	// keys — connected:false from the refusal path, error from a failed
	// dispatch — so read the keys, never run's wording.
	if err != nil || res.Output == "" {
		return res, err
	}
	if v, ok := res.Metadata["connected"].(bool); ok && !v {
		return res, nil
	}
	if _, ok := res.Metadata["error"]; ok {
		return res, nil
	}
	var shot struct {
		Base64 string `json:"base64"`
		URL    string `json:"url"`
		Format string `json:"format"` // media type of the delivered image; older extensions omit it (PNG)
	}
	if err := json.Unmarshal([]byte(res.Output), &shot); err != nil || shot.Base64 == "" {
		return Result{Title: "Browser screenshot", Output: "The extension returned no image data."}, nil
	}
	media := shot.Format
	if media == "" {
		media = "image/png"
	}
	return Result{
		Title:  "Browser screenshot",
		Output: "Screenshot captured" + screenshotWhere(shot.URL) + "; the image is attached.",
		Image:  &ResultImage{MediaType: media, Data: shot.Base64},
	}, nil
}

func screenshotWhere(url string) string {
	if url == "" {
		return ""
	}
	return " of " + url
}

// BrowserNetworkTool reads the network traffic a driven tab captured.
type BrowserNetworkTool struct{ Disp BrowserDispatcher }

func (BrowserNetworkTool) ID() string { return "browser_network" }

func (BrowserNetworkTool) Description() string {
	return "Read captured network requests from a granted browser tab: URL, method, status, type, sizes and timings, plus one captured response body by requestId. Capture starts with the first browser op on the tab (the debugger then stays attached while you drive it) and covers page loads and fetches since that moment. Use it to debug the API calls, redirects and failed requests a page makes."
}

func (BrowserNetworkTool) Parameters() json.RawMessage {
	return browserParams(`"url":{"type":"string","description":"Return only requests whose URL contains this substring."},"requestId":{"type":"string","description":"Fetch this request's response body instead of listing entries (id taken from a previous browser_network call)."},"max":{"type":"integer","description":"How many entries to return, oldest first, newest last (default 100, hard max 300)."},` + tabSchema)
}

func (t BrowserNetworkTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		URL       string `json:"url"`
		RequestID string `json:"requestId"`
		Max       int    `json:"max"`
		Tab       *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_network", err), nil
	}
	callArgs := map[string]any{}
	if in.URL != "" {
		callArgs["url"] = in.URL
	}
	if in.RequestID != "" {
		callArgs["requestId"] = in.RequestID
	}
	if in.Max > 0 {
		callArgs["max"] = in.Max
	}
	return browserBase{t.Disp}.run(ctx, "Browser network", browser.OpNetwork, callArgs, in.Tab)
}

// BrowserConsoleTool reads the console output a driven tab captured.
type BrowserConsoleTool struct{ Disp BrowserDispatcher }

func (BrowserConsoleTool) ID() string { return "browser_console" }

func (BrowserConsoleTool) Description() string {
	return "Read captured console output from a granted browser tab: console.log/warning/error calls, uncaught page exceptions, and browser log entries (CSP, network and security notices). Capture starts with the first browser op on the tab. Use it to see the JavaScript errors a page hits while you drive it."
}

func (BrowserConsoleTool) Parameters() json.RawMessage {
	return browserParams(`"level":{"type":"string","description":"Return only entries at this level (log, info, warning, error, debug)."},"source":{"type":"string","description":"Return only entries from this source (console, exception, or browser-log)."},"max":{"type":"integer","description":"How many entries to return, oldest first, newest last (default 100, hard max 300)."},` + tabSchema)
}

func (t BrowserConsoleTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var in struct {
		Level  string `json:"level"`
		Source string `json:"source"`
		Max    int    `json:"max"`
		Tab    *int64 `json:"tab"`
	}
	if err := DecodeArgs(args, &in); err != nil {
		return browserBase{}.malformed("browser_console", err), nil
	}
	callArgs := map[string]any{}
	if in.Level != "" {
		callArgs["level"] = in.Level
	}
	if in.Source != "" {
		callArgs["source"] = in.Source
	}
	if in.Max > 0 {
		callArgs["max"] = in.Max
	}
	return browserBase{t.Disp}.run(ctx, "Browser console", browser.OpConsole, callArgs, in.Tab)
}

// malformed shapes one arg-decode failure the way run shapes op failures.
func (browserBase) malformed(name string, err error) Result {
	return Result{Title: "Browser " + strings.TrimPrefix(name, "browser_"), Output: fmt.Sprintf("%s: bad arguments: %v", name, err)}
}

// NewBrowserTools returns the takeover tool set for registration.
func NewBrowserTools(disp BrowserDispatcher) []ToolDef {
	return []ToolDef{
		BrowserStatusTool{Disp: disp},
		BrowserNavigateTool{Disp: disp},
		BrowserSnapshotTool{Disp: disp},
		BrowserContentTool{Disp: disp},
		BrowserClickTool{Disp: disp},
		BrowserTypeTool{Disp: disp},
		BrowserKeyTool{Disp: disp},
		BrowserScrollTool{Disp: disp},
		BrowserScreenshotTool{Disp: disp},
		BrowserNetworkTool{Disp: disp},
		BrowserConsoleTool{Disp: disp},
	}
}

// BrowserToolIDs lists the takeover tool IDs in NewBrowserTools registration
// order. It exists so the agent loop can offer the whole family on the wire at
// once — when the intent matcher names the browser-takeover skill for a plan
// step, or the agent loads that skill — instead of the globs expanding it
// turn after turn.
func BrowserToolIDs() []string {
	return []string{
		"browser_status",
		"browser_navigate",
		"browser_snapshot",
		"browser_content",
		"browser_click",
		"browser_type",
		"browser_key",
		"browser_scroll",
		"browser_screenshot",
		"browser_network",
		"browser_console",
	}
}
