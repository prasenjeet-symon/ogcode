package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// analyzeImageCounter stands in for agent.LoopRunner.RunVisionSession: it
// counts invocations and answers with the call number, so a cache hit (Run
// never called) is distinguishable from a real analysis round trip, mirroring
// deepSearchCounter. It keeps the last payload and mode so tests can assert
// what actually went out.
type analyzeImageCounter struct {
	mu            sync.Mutex
	calls         int
	lastImage     []byte
	lastMediaType string
	lastMode      string
	lastModel     string
	lastProvider  string
	lastEffort    string
}

func (c *analyzeImageCounter) run(_ context.Context, imageBytes []byte, mediaType, mode, _, model, provider, effort string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastImage = append([]byte(nil), imageBytes...)
	c.lastMediaType, c.lastMode = mediaType, mode
	c.lastModel, c.lastProvider = model, provider
	c.lastEffort = effort
	return fmt.Sprintf("answer #%d", c.calls), nil
}

func (c *analyzeImageCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// analyzeImageInput is one Execute call: the arguments plus the ToolContext
// fields that ride into the cache key.
type analyzeImageInput struct {
	path     string
	question string
	mode     string
	fresh    bool
	effort   string
	model    string
	provider string
	// ctxEffort is the run's resolved effort, the level a call with no effort
	// of its own inherits.
	ctxEffort  string
	sessionDir string
}

func (in analyzeImageInput) args(t *testing.T) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(struct {
		Path     string `json:"path"`
		Question string `json:"question"`
		Mode     string `json:"mode"`
		Fresh    bool   `json:"fresh"`
		Effort   string `json:"effort"`
	}{Path: in.path, Question: in.question, Mode: in.mode, Fresh: in.fresh, Effort: in.effort})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

func (in analyzeImageInput) toolContext() Context {
	return Context{Model: in.model, Provider: in.provider, Effort: in.ctxEffort, SessionDir: in.sessionDir}
}

// analyzeImageTestPNG renders a one-pixel PNG, the smallest image the tool
// passes through unchanged.
func analyzeImageTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// analyzeImageWriteFile lays a file down in its own temp session directory
// and returns the workspace-relative name and the directory, the shape a
// calling agent actually hands the tool.
func analyzeImageWriteFile(t *testing.T, name string, data []byte) (name_, dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return name, dir
}

func TestAnalyzeImageToolModeValidation(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

	for _, mode := range []string{"describe", "transcribe", "answer", ""} {
		in := analyzeImageInput{path: name, sessionDir: dir, model: "m", provider: "p", mode: mode}
		if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
			t.Fatalf("mode %q: unexpected error: %v", mode, err)
		}
	}
	in := analyzeImageInput{path: name, sessionDir: dir, model: "m", provider: "p", mode: "read aloud"}
	if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err == nil {
		t.Fatal("an unknown mode was accepted")
	} else if !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("error = %v, want it to name the invalid mode", err)
	}
	// Four accepted mode strings but only two distinct effective modes:
	// describe and transcribe run; the plain answer (nothing to ask) and
	// the empty-mode default both degrade to describe, so both hit the
	// describe entry the first call wrote — the key carries the effective
	// mode. The answer-degrades-to-describe chain is pinned in detail by
	// TestAnalyzeImageToolAnswerBlankQuestionSharesDescribeCache.
	if got := run.count(); got != 2 {
		t.Fatalf("Run called %d times for four valid mode strings, want 2 (answer with nothing to ask shares the describe entry)", got)
	}

	// An empty mode still defaults to answer when there IS a question to
	// ask: different bytes miss every earlier entry and run as "answer".
	different := analyzeImageTestPNG(t)
	different[len(different)-1] ^= 0xff
	if err := os.WriteFile(filepath.Join(dir, name), different, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	qin := analyzeImageInput{path: name, sessionDir: dir, model: "m", provider: "p", mode: "", question: "what is shown"}
	if _, err := tool.Execute(context.Background(), qin.args(t), qin.toolContext()); err != nil {
		t.Fatalf("empty mode with a question: unexpected error: %v", err)
	}
	if run.lastMode != "answer" {
		t.Fatalf("an empty mode with a question did not default to answer (last mode %q)", run.lastMode)
	}
}

func TestAnalyzeImageToolPathResolution(t *testing.T) {
	tool := AnalyzeImageTool{Run: (&analyzeImageCounter{}).run, Cache: deepSearchTestCache(t)}
	// A workspace-relative name is resolved against the session directory;
	// a name nothing serves is an error, not an empty result.
	in := analyzeImageInput{path: "missing.png", sessionDir: t.TempDir()}
	if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err == nil {
		t.Fatal("a missing file was silently accepted")
	} else if !strings.Contains(err.Error(), "read image") {
		t.Fatalf("error = %v, want a read failure naming the file", err)
	}
}

// The cache key hashes the file's CONTENT: a second call on the same bytes is
// served without touching the model, a call on different bytes is not, and
// fresh=true bypasses the read while still refreshing the entry it produces.
func TestAnalyzeImageToolCachesByContent(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))
	in := analyzeImageInput{path: name, mode: "transcribe", model: "m", provider: "p", sessionDir: dir}

	for i := 0; i < 2; i++ {
		res, err := tool.Execute(context.Background(), in.args(t), in.toolContext())
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if cached, ok := res.Metadata["analyzeImageCached"].(bool); !ok || cached != (i == 1) {
			t.Fatalf("call %d: analyzeImageCached = %v", i+1, res.Metadata["analyzeImageCached"])
		}
	}
	// A second image at the same name with different bytes must miss: the
	// content hash, not the path, is the key.
	different := analyzeImageTestPNG(t)
	different[len(different)-1] ^= 0xff
	if err := os.WriteFile(filepath.Join(dir, name), different, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
		t.Fatalf("different content: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); cached {
		t.Fatal("different content was served from the same cache entry")
	}
	if got := run.count(); got != 2 {
		t.Fatalf("Run called %d times after two same-content calls and one different-content call, want 2", got)
	}

	// fresh=true bypasses the read and refreshes the entry for the next
	// ordinary call.
	fresh := in
	fresh.fresh = true
	if _, err := tool.Execute(context.Background(), fresh.args(t), fresh.toolContext()); err != nil {
		t.Fatalf("fresh call: %v", err)
	}
	if got := run.count(); got != 3 {
		t.Fatalf("fresh bypass did not call Run (count %d, want 3)", got)
	}
	res, err := tool.Execute(context.Background(), in.args(t), in.toolContext())
	if err != nil {
		t.Fatalf("post-fresh call: %v", err)
	}
	if got := run.count(); got != 3 {
		t.Fatalf("post-fresh call missed the refreshed entry (count %d, want 3)", got)
	}
	if cached, _ := res.Metadata["analyzeImageCached"].(bool); !cached {
		t.Fatal("post-fresh call was not served from cache")
	}
	if !strings.Contains(res.Output, "fresh=true") {
		t.Fatalf("cached output %q does not carry the cache footer", res.Output)
	}
}

func TestAnalyzeImageToolUnsupportedMediaType(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "notes.txt", []byte("plain text, not an image"))

	in := analyzeImageInput{path: name, mode: "describe", sessionDir: dir}
	_, err := tool.Execute(context.Background(), in.args(t), in.toolContext())
	if err == nil {
		t.Fatal("a text file was accepted as an image")
	}
	if !strings.Contains(err.Error(), "unsupported or unreadable image") {
		t.Fatalf("error = %v, want the unsupported-type message", err)
	}
	if run.count() != 0 {
		t.Fatalf("Run was called %d times for an unsupported file", run.count())
	}
}

func TestAnalyzeImageToolSendsPNGThroughUnchanged(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	data := analyzeImageTestPNG(t)
	name, dir := analyzeImageWriteFile(t, "shot.png", data)

	in := analyzeImageInput{path: name, mode: "answer", question: "what is this?", sessionDir: dir}
	res, err := tool.Execute(context.Background(), in.args(t), in.toolContext())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if med := run.lastMediaType; med != "image/png" {
		t.Fatalf("media type = %q, want image/png passthrough", med)
	}
	if !bytes.Equal(run.lastImage, data) {
		t.Fatal("payload was re-encoded instead of passed through")
	}
	if run.lastMode != "answer" {
		t.Fatalf("mode = %q, want answer", run.lastMode)
	}
	if !strings.HasPrefix(res.Output, "answer #1") {
		t.Fatalf("output = %q, want the extracted text first", res.Output)
	}
	if res.Output != "answer #1" {
		t.Fatalf("fresh output = %q, want Run output verbatim without a duplicate tool footer", res.Output)
	}
}

// When ResolveModel overrides the session's inherited model (e.g. a
// settings-screen image-analysis model), the cache key must track the
// resolved model rather than the inherited one so changing the preference
// invalidates stale hits.
func TestAnalyzeImageToolCacheKeyUsesResolvedModel(t *testing.T) {
	run := &analyzeImageCounter{}
	resolvedModel := "vision-a"
	tool := AnalyzeImageTool{
		Run: run.run,
		ResolveModel: func(_, _ string) (string, string) {
			return resolvedModel, "vision-prov"
		},
		Cache: deepSearchTestCache(t),
	}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))
	in := analyzeImageInput{path: name, mode: "transcribe", model: "blind-session", provider: "session-prov", sessionDir: dir}

	if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if run.lastModel != "vision-a" || run.lastProvider != "vision-prov" {
		t.Fatalf("Run got (%q, %q), want (vision-a, vision-prov)", run.lastModel, run.lastProvider)
	}

	// Same resolved model hits the cache.
	if res, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
		t.Fatalf("second call: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); !cached {
		t.Fatal("same resolved model missed the cache")
	}

	// Switching the resolved vision model must miss even though the session
	// model stayed identical.
	resolvedModel = "vision-b"
	if res, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
		t.Fatalf("third call: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); cached {
		t.Fatal("switching the resolved vision model hit the previous model's cache entry")
	}
	if got := run.count(); got != 2 {
		t.Fatalf("Run called %d times, want 2", got)
	}
	if run.lastModel != "vision-b" {
		t.Fatalf("Run got model %q, want vision-b", run.lastModel)
	}
}

// An answer with nothing to ask actually runs a describe: the vision
// pipeline degrades answer-with-blank-question to describe
// (RunVisionSession), so the tool must key the cache by that effective
// mode — otherwise the plain describe below re-runs the same picture and
// it is stored under two entries.
func TestAnalyzeImageToolAnswerBlankQuestionSharesDescribeCache(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

	whitespace := analyzeImageInput{path: name, mode: "answer", question: "  \t ", model: "m", provider: "p", sessionDir: dir}
	res, err := tool.Execute(context.Background(), whitespace.args(t), whitespace.toolContext())
	if err != nil {
		t.Fatalf("answer call with whitespace question: %v", err)
	}
	if run.lastMode != "describe" {
		t.Fatalf("Run got mode %q, want describe (nothing to answer runs a describe)", run.lastMode)
	}
	if res.Metadata["analyzeImageMode"] != "describe" {
		t.Fatalf("analyzeImageMode = %v, want describe", res.Metadata["analyzeImageMode"])
	}

	empty := whitespace
	empty.question = ""
	if _, err := tool.Execute(context.Background(), empty.args(t), empty.toolContext()); err != nil {
		t.Fatalf("answer call with empty question: %v", err)
	}
	describe := whitespace
	describe.mode, describe.question = "describe", ""
	res, err = tool.Execute(context.Background(), describe.args(t), describe.toolContext())
	if err != nil {
		t.Fatalf("describe call: %v", err)
	}
	if cached, ok := res.Metadata["analyzeImageCached"].(bool); !ok || !cached {
		t.Fatalf("describe call missed the entry the blank-question answer wrote (analyzeImageCached = %v)", res.Metadata["analyzeImageCached"])
	}
	if !strings.Contains(res.Output, "Served the cached describe") {
		t.Fatalf("cached output %q does not name the describe mode", res.Output)
	}
	if got := run.count(); got != 1 {
		t.Fatalf("Run called %d times, want 1: the same picture must not be analyzed twice", got)
	}
}

// The question changes what is asked only in answer mode — the schema
// documents it as ignored otherwise, and the pipeline never reads it for
// transcribe or describe — so a describe carrying a question must share
// the entry a plain describe wrote.
func TestAnalyzeImageToolDescribeIgnoresQuestionInCacheKey(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

	asked := analyzeImageInput{path: name, mode: "describe", question: "is there a cat in this picture", model: "m", provider: "p", sessionDir: dir}
	if _, err := tool.Execute(context.Background(), asked.args(t), asked.toolContext()); err != nil {
		t.Fatalf("describe call with question: %v", err)
	}
	if run.lastMode != "describe" {
		t.Fatalf("Run got mode %q, want describe", run.lastMode)
	}

	plain := asked
	plain.question = ""
	res, err := tool.Execute(context.Background(), plain.args(t), plain.toolContext())
	if err != nil {
		t.Fatalf("plain describe call: %v", err)
	}
	if cached, ok := res.Metadata["analyzeImageCached"].(bool); !ok || !cached {
		t.Fatalf("plain describe missed the entry written with a question (analyzeImageCached = %v)", res.Metadata["analyzeImageCached"])
	}
	if got := run.count(); got != 1 {
		t.Fatalf("Run called %d times, want 1: the question must not split describe entries", got)
	}
}

// Different non-empty questions ask different things, so their answer-mode
// results stay separate cache entries — but the same question again is a
// hit, pinning that the question participates in the key.
func TestAnalyzeImageToolAnswerKeyIncludesQuestion(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

	first := analyzeImageInput{path: name, mode: "answer", question: "what is shown", model: "m", provider: "p", sessionDir: dir}
	if _, err := tool.Execute(context.Background(), first.args(t), first.toolContext()); err != nil {
		t.Fatalf("first answer: %v", err)
	}

	second := first
	second.question = "who signed the page"
	if res, err := tool.Execute(context.Background(), second.args(t), second.toolContext()); err != nil {
		t.Fatalf("second answer: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); cached {
		t.Fatal("a different question hit the first question's cache entry")
	}

	res, err := tool.Execute(context.Background(), first.args(t), first.toolContext())
	if err != nil {
		t.Fatalf("repeat answer: %v", err)
	}
	if cached, _ := res.Metadata["analyzeImageCached"].(bool); !cached {
		t.Fatal("the same question missed the entry it wrote")
	}
	if got := run.count(); got != 2 {
		t.Fatalf("Run called %d times, want 2", got)
	}
}

// The wire contract has no model or provider argument: the model is resolved
// server-side from settings, never named by the calling agent.
func TestAnalyzeImageToolSchemaHasNoModelArg(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(AnalyzeImageTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	for _, banned := range []string{"model", "provider", "providerId"} {
		if _, ok := schema.Properties[banned]; ok {
			t.Fatalf("schema property %q exists — the model must be resolved server-side", banned)
		}
	}
	for _, want := range []string{"path", "question", "mode", "fresh", "effort"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("schema is missing the %q property", want)
		}
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Fatalf("required = %v, want only path", schema.Required)
	}
	if detected := http.DetectContentType(analyzeImageTestPNG(t)); detected != "image/png" {
		t.Fatalf("test fixture detected as %q, want image/png", detected)
	}
}

// The schema offers only the three depths the tool accepts, so a model never
// picks a level whose call would then be rejected.
func TestAnalyzeImageToolEffortEnumIsTheThreeDepths(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(AnalyzeImageTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	want := []string{"low", "medium", "high"}
	if got := schema.Properties["effort"].Enum; !slices.Equal(got, want) {
		t.Fatalf("effort enum = %v, want %v", got, want)
	}
}

// A level outside the three depths is a mistake to report, not a value to
// send: most hosts reject an effort they do not know, and the finer rungs of
// the session ladder (minimal, xhigh, max) are not the tool's vocabulary.
func TestAnalyzeImageToolRejectsUnknownEffort(t *testing.T) {
	for _, effort := range []string{"turbo", "none", "on", "minimal", "xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			run := &analyzeImageCounter{}
			tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
			name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

			in := analyzeImageInput{path: name, mode: "describe", effort: effort, sessionDir: dir}
			if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err == nil {
				t.Fatal("an unknown effort was accepted")
			} else if !strings.Contains(err.Error(), "invalid effort") {
				t.Fatalf("error = %v, want the invalid-effort message", err)
			}
			if got := run.count(); got != 0 {
				t.Fatalf("Run was called %d times for an invalid effort", got)
			}
		})
	}
}

// The call's own effort wins over the run's, so one analysis can think harder
// (or stop thinking) without moving the whole session; a call that names none
// inherits the run's level.
func TestAnalyzeImageToolEffortOverridesInherited(t *testing.T) {
	tests := []struct {
		name      string
		effort    string
		ctxEffort string
		want      string
	}{
		{"own level wins", "high", "low", "high"},
		{"no own level inherits", "", "low", "low"},
		{"neither is set", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &analyzeImageCounter{}
			tool := AnalyzeImageTool{Run: run.run}
			name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))
			in := analyzeImageInput{path: name, mode: "describe", effort: tt.effort, ctxEffort: tt.ctxEffort,
				model: "m", provider: "p", sessionDir: dir}

			if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext()); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if run.lastEffort != tt.want {
				t.Fatalf("Run got effort %q, want %q", run.lastEffort, tt.want)
			}
		})
	}
}

// The effort rides the cache key: a deep diagnosis and a quick caption of the
// same picture are different answers, so a higher-effort call must not be
// served the lower-effort entry — while the same level again is a hit, whether
// it was named or inherited.
func TestAnalyzeImageToolCacheKeyIncludesEffort(t *testing.T) {
	run := &analyzeImageCounter{}
	tool := AnalyzeImageTool{Run: run.run, Cache: deepSearchTestCache(t)}
	name, dir := analyzeImageWriteFile(t, "shot.png", analyzeImageTestPNG(t))

	low := analyzeImageInput{path: name, mode: "describe", effort: "low", model: "m", provider: "p", sessionDir: dir}
	if _, err := tool.Execute(context.Background(), low.args(t), low.toolContext()); err != nil {
		t.Fatalf("low call: %v", err)
	}

	high := low
	high.effort = "high"
	if res, err := tool.Execute(context.Background(), high.args(t), high.toolContext()); err != nil {
		t.Fatalf("high call: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); cached {
		t.Fatal("a higher-effort call was served the lower-effort entry")
	}

	res, err := tool.Execute(context.Background(), high.args(t), high.toolContext())
	if err != nil {
		t.Fatalf("repeat high call: %v", err)
	}
	if cached, _ := res.Metadata["analyzeImageCached"].(bool); !cached {
		t.Fatal("the same effort missed the entry it wrote")
	}

	// Inheriting the level an entry was written at is the same call.
	inherited := low
	inherited.effort = ""
	inherited.ctxEffort = "low"
	if res, err := tool.Execute(context.Background(), inherited.args(t), inherited.toolContext()); err != nil {
		t.Fatalf("inherited call: %v", err)
	} else if cached, _ := res.Metadata["analyzeImageCached"].(bool); !cached {
		t.Fatal("inheriting the level an entry was written at missed it")
	}

	if got := run.count(); got != 2 {
		t.Fatalf("Run called %d times, want 2", got)
	}
}
