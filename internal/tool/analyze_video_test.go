package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// videoRunCounter stands in for agent.LoopRunner.RunVideoSession: it counts
// invocations and records the request, so a cache hit (Run never called) is
// distinguishable from a real analysis, mirroring analyzeImageCounter.
type videoRunCounter struct {
	mu      sync.Mutex
	calls   int
	lastReq VideoAnalysisRequest
}

func (c *videoRunCounter) run(_ context.Context, req VideoAnalysisRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastReq = req
	return fmt.Sprintf("## Timeline\n\n- %s: screen changes", req.Frames[0].Label), nil
}

func (c *videoRunCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *videoRunCounter) request() VideoAnalysisRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReq
}

// videoTestExtractor writes a small prepared manifest into opts.OutDir, so the
// tool's own logic — modes, cache, frame listing, manifest reuse — is exercised
// without ffmpeg. It counts its calls so reuse is observable.
func videoTestExtractor(t *testing.T, calls *int) func(context.Context, VideoExtractOptions) (*VideoManifest, error) {
	t.Helper()
	return func(_ context.Context, opts VideoExtractOptions) (*VideoManifest, error) {
		*calls++
		framesDir := filepath.Join(opts.OutDir, "frames")
		if err := os.MkdirAll(framesDir, 0o755); err != nil {
			return nil, err
		}
		seconds := []float64{1, 64, 125}
		frames := make([]VideoFrameInfo, 0, len(seconds))
		for i, s := range seconds {
			name := fmt.Sprintf("%06d_%s.jpg", i+1, videoFileStamp(s))
			path := filepath.Join(framesDir, name)
			if err := os.WriteFile(path, []byte("jpeg-bytes"), 0o644); err != nil {
				return nil, err
			}
			frames = append(frames, VideoFrameInfo{
				Index:   i + 1,
				Seconds: s,
				Stamp:   videoStamp(s),
				Label:   "[" + videoStamp(s) + "]",
				Path:    path,
				Change:  float64(i) * 0.035,
				Dropped: 12,
			})
		}
		manifest := &VideoManifest{
			Source:           opts.Path,
			DurationSec:      126,
			SampleFPS:        opts.SampleFPS,
			Sampled:          126,
			Kept:             len(frames),
			Dropped:          120,
			DuplicateDropped: 120,
			Frames:           frames,
			FramesDir:        framesDir,
			ManifestPath:     filepath.Join(opts.OutDir, "manifest.json"),
		}
		body, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(manifest.ManifestPath, body, 0o644); err != nil {
			return nil, err
		}
		return manifest, nil
	}
}

// videoTestFile lays a stand-in recording in its own temp session directory and
// returns the workspace-relative name and that directory.
func videoTestFile(t *testing.T) (name, dir string) {
	t.Helper()
	dir = t.TempDir()
	name = "recording.mp4"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not really a video"), 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	return name, dir
}

func videoTestArgs(t *testing.T, path, mode, question string, fresh bool) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(struct {
		Path     string `json:"path"`
		Mode     string `json:"mode"`
		Question string `json:"question"`
		Fresh    bool   `json:"fresh"`
	}{Path: path, Mode: mode, Question: question, Fresh: fresh})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

func TestAnalyzeVideoToolSchemaRequiresPath(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(AnalyzeVideoTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if !slices.Contains(schema.Required, "path") {
		t.Fatalf("required = %v, want path", schema.Required)
	}
	if got := schema.Properties["mode"].Enum; !slices.Equal(got, []string{"describe", "transcribe", "answer"}) {
		t.Fatalf("mode enum = %v", got)
	}
	if got := schema.Properties["effort"].Enum; !slices.Equal(got, []string{"low", "medium", "high"}) {
		t.Fatalf("effort enum = %v", got)
	}
	for _, name := range []string{"segment", "segment_frames"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("schema is missing the %q property", name)
		}
	}
}

func TestAnalyzeVideoToolBlankPath(t *testing.T) {
	tool := AnalyzeVideoTool{Run: (&videoRunCounter{}).run}
	res, err := tool.Execute(context.Background(), videoTestArgs(t, "", "", "", false), Context{SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("blank path: %v", err)
	}
	if res.Output != "path is required" {
		t.Fatalf("output = %q, want the path hint", res.Output)
	}
}

func TestAnalyzeVideoToolMissingFile(t *testing.T) {
	tool := AnalyzeVideoTool{Run: (&videoRunCounter{}).run}
	_, err := tool.Execute(context.Background(), videoTestArgs(t, "missing.mp4", "", "", false), Context{SessionDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "read video") {
		t.Fatalf("error = %v, want a read failure naming the video", err)
	}
}

func TestAnalyzeVideoToolRejectsUnknownModeAndEffort(t *testing.T) {
	name, dir := videoTestFile(t)
	tool := AnalyzeVideoTool{Run: (&videoRunCounter{}).run}
	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "summarize", "", false), Context{SessionDir: dir}); err == nil {
		t.Fatal("an unsupported mode was accepted")
	}
	args, err := json.Marshal(struct {
		Path   string `json:"path"`
		Effort string `json:"effort"`
	}{Path: name, Effort: "extreme"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args, Context{SessionDir: dir}); err == nil {
		t.Fatal("an unsupported effort was accepted")
	}
}

// An answer with no question runs a describe with the question dropped, and a
// question offered to describe is ignored — the request the pipeline sees must
// match the mode the cache key was built from.
func TestAnalyzeVideoToolModeNormalization(t *testing.T) {
	name, dir := videoTestFile(t)
	run := &videoRunCounter{}
	extracts := 0
	tool := AnalyzeVideoTool{Run: run.run, Extract: videoTestExtractor(t, &extracts)}

	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "answer", "   ", false), Context{SessionDir: dir}); err != nil {
		t.Fatalf("blank answer: %v", err)
	}
	if req := run.request(); req.Mode != analyzeVideoModeDescribe || req.Question != "" {
		t.Fatalf("blank answer ran mode %q question %q, want describe with no question", req.Mode, req.Question)
	}

	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "what is on screen?", false), Context{SessionDir: dir}); err != nil {
		t.Fatalf("describe with question: %v", err)
	}
	if req := run.request(); req.Mode != analyzeVideoModeDescribe || req.Question != "" {
		t.Fatalf("describe ran mode %q question %q, want the question dropped", req.Mode, req.Question)
	}

	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "answer", "which app is open?", false), Context{SessionDir: dir}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if req := run.request(); req.Mode != analyzeVideoModeAnswer || req.Question != "which app is open?" {
		t.Fatalf("answer ran mode %q question %q", req.Mode, req.Question)
	}
}

// The cache key is the recording's identity, mode, question and resolved model:
// a second call is served without the pipeline, fresh=true bypasses it, and the
// refreshed entry serves the next ordinary call.
func TestAnalyzeVideoToolCachesByRecordingIdentity(t *testing.T) {
	name, dir := videoTestFile(t)
	run := &videoRunCounter{}
	extracts := 0
	tool := AnalyzeVideoTool{Run: run.run, Extract: videoTestExtractor(t, &extracts), Cache: deepSearchTestCache(t)}
	in := Context{SessionDir: dir, Model: "m", Provider: "p"}

	for i := 0; i < 2; i++ {
		res, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "", false), in)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if cached, ok := res.Metadata["analyzeVideoCached"].(bool); !ok || cached != (i == 1) {
			t.Fatalf("call %d: analyzeVideoCached = %v", i+1, res.Metadata["analyzeVideoCached"])
		}
	}
	if got := run.count(); got != 1 {
		t.Fatalf("Run called %d times for two identical calls, want 1", got)
	}

	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "", true), in); err != nil {
		t.Fatalf("fresh call: %v", err)
	}
	if got := run.count(); got != 2 {
		t.Fatalf("fresh bypass did not call Run (count %d, want 2)", got)
	}
	// The frames were extracted once and reused from the manifest, so a fresh
	// analysis decodes no video.
	if extracts != 1 {
		t.Fatalf("extractor ran %d times, want 1 (frames reused from the manifest)", extracts)
	}

	res, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "", false), in)
	if err != nil {
		t.Fatalf("post-fresh call: %v", err)
	}
	if got := run.count(); got != 2 {
		t.Fatalf("post-fresh call missed the refreshed entry (count %d, want 2)", got)
	}
	if cached, _ := res.Metadata["analyzeVideoCached"].(bool); !cached {
		t.Fatal("post-fresh call was not served from cache")
	}
	if !strings.Contains(res.Output, "fresh=true") {
		t.Fatalf("cached output %q does not carry the cache footer", res.Output)
	}
}

// The result must hand the agent the timeline plus one line per kept frame, so
// a follow-up view_image or analyze_image call has a path to open.
func TestAnalyzeVideoToolFrameListing(t *testing.T) {
	name, dir := videoTestFile(t)
	extracts := 0
	tool := AnalyzeVideoTool{Run: (&videoRunCounter{}).run, Extract: videoTestExtractor(t, &extracts)}

	res, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "", false), Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "## Timeline") {
		t.Fatalf("output %q lost the pipeline's text", res.Output)
	}
	if !strings.Contains(res.Output, "### Frames") {
		t.Fatalf("output %q has no frame listing", res.Output)
	}
	for _, want := range []string{"[00:00:01]", "[00:01:04]", "[00:02:05]", "(change 3.5%)"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("frame listing missing %q:\n%s", want, res.Output)
		}
	}
	if got, _ := res.Metadata["analyzeVideoFrames"].(int); got != 3 {
		t.Fatalf("analyzeVideoFrames = %v, want 3", res.Metadata["analyzeVideoFrames"])
	}
	if !strings.HasPrefix(res.Title, "Analyze video: ") {
		t.Fatalf("title = %q", res.Title)
	}
}

// The request the pipeline receives carries the recording's identity, the
// resolved model and the kept frames in order — the labels the model reads come
// from the extractor's timestamps.
func TestAnalyzeVideoToolBuildsPipelineRequest(t *testing.T) {
	name, dir := videoTestFile(t)
	run := &videoRunCounter{}
	extracts := 0
	tool := AnalyzeVideoTool{
		Run:     run.run,
		Extract: videoTestExtractor(t, &extracts),
		ResolveModel: func(inheritedModel, inheritedProvider string) (string, string) {
			return "vision-model", "vision-provider"
		},
	}
	in := Context{SessionDir: dir, Model: "session-model", Provider: "session-provider", Effort: "high"}
	if _, err := tool.Execute(context.Background(), videoTestArgs(t, name, "describe", "", false), in); err != nil {
		t.Fatalf("execute: %v", err)
	}
	req := run.request()
	if req.Title != name || req.Duration != "00:02:06" {
		t.Fatalf("request title/duration = %q/%q", req.Title, req.Duration)
	}
	if req.Model != "vision-model" || req.ProviderID != "vision-provider" || req.Effort != "high" {
		t.Fatalf("request resolved model = %q/%q effort %q", req.Model, req.ProviderID, req.Effort)
	}
	if len(req.Frames) != 3 {
		t.Fatalf("request frames = %d, want 3", len(req.Frames))
	}
	wantLabels := []string{"[00:00:01]", "[00:01:04]", "[00:02:05]"}
	for i, f := range req.Frames {
		if f.Label != wantLabels[i] {
			t.Fatalf("frame %d label = %q, want %q", i, f.Label, wantLabels[i])
		}
		if f.MediaType != "image/jpeg" || f.Base64 == "" {
			t.Fatalf("frame %d payload = %q/%q", i, f.MediaType, f.Base64)
		}
	}
}

// videoSegmentArgs builds a call with a focus window, the shape the agent's
// segment/segment_frames parameters produce.
func videoSegmentArgs(t *testing.T, path, segment string, segmentFrames int, fresh bool) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(struct {
		Path          string `json:"path"`
		Segment       string `json:"segment"`
		SegmentFrames int    `json:"segment_frames"`
		Fresh         bool   `json:"fresh"`
	}{Path: path, Segment: segment, SegmentFrames: segmentFrames, Fresh: fresh})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

// The focus window reaches the extractor as seconds with the default detail
// budget, and the pipeline learns the window's label so the model knows why the
// frames bunch up there.
func TestAnalyzeVideoToolForwardsSegment(t *testing.T) {
	name, dir := videoTestFile(t)
	run := &videoRunCounter{}
	extracts := 0
	next := videoTestExtractor(t, &extracts)
	var got VideoExtractOptions
	called := false
	tool := AnalyzeVideoTool{
		Run: run.run,
		Extract: func(ctx context.Context, opts VideoExtractOptions) (*VideoManifest, error) {
			called = true
			got = opts
			return next(ctx, opts)
		},
	}
	if _, err := tool.Execute(context.Background(), videoSegmentArgs(t, name, "04:10-04:40", 0, false), Context{SessionDir: dir}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Fatal("the extractor was never called")
	}
	if got.SegmentStart != 250 || got.SegmentEnd != 280 {
		t.Fatalf("extract window = %g–%g, want 250–280", got.SegmentStart, got.SegmentEnd)
	}
	if got.FocusFrames != analyzeVideoDefaultFocusFrames {
		t.Fatalf("extract focus frames = %d, want the default %d", got.FocusFrames, analyzeVideoDefaultFocusFrames)
	}
	if req := run.request(); req.Segment != "00:04:10–00:04:40" {
		t.Fatalf("request segment = %q, want the window label", req.Segment)
	}
}

// The cache key and the scratch directory both carry the window and the detail
// budget: an unchanged recording with a new window is new work, while a repeat
// of the same window is a cache hit and then a manifest reuse.
func TestAnalyzeVideoToolSegmentKeysCache(t *testing.T) {
	name, dir := videoTestFile(t)
	run := &videoRunCounter{}
	extracts := 0
	tool := AnalyzeVideoTool{
		Run:     run.run,
		Extract: videoTestExtractor(t, &extracts),
		Cache:   deepSearchTestCache(t),
	}
	in := Context{SessionDir: dir}
	exec := func(segment string, fresh bool) {
		t.Helper()
		if _, err := tool.Execute(context.Background(), videoSegmentArgs(t, name, segment, 0, fresh), in); err != nil {
			t.Fatalf("execute(%q): %v", segment, err)
		}
	}

	exec("04:10-04:40", false)
	if extracts != 1 || run.count() != 1 {
		t.Fatalf("first call: extracted %d, ran %d; want 1 and 1", extracts, run.count())
	}
	exec("04:10-04:40", false)
	if extracts != 1 || run.count() != 1 {
		t.Fatalf("repeat served nothing from cache: extracted %d, ran %d", extracts, run.count())
	}
	exec("04:12-04:40", false)
	if extracts != 2 || run.count() != 2 {
		t.Fatalf("a new window reused the first: extracted %d, ran %d; want 2 and 2", extracts, run.count())
	}
	exec("04:12-04:40", true)
	if extracts != 2 || run.count() != 3 {
		t.Fatalf("fresh re-extracted an unchanged window: extracted %d, ran %d; want 2 and 3", extracts, run.count())
	}
}

func TestAnalyzeVideoToolRejectsInvalidSegment(t *testing.T) {
	name, dir := videoTestFile(t)
	runs, extracts := 0, 0
	tool := AnalyzeVideoTool{
		Run: func(context.Context, VideoAnalysisRequest) (string, error) {
			runs++
			return "timeline", nil
		},
		Extract: videoTestExtractor(t, &extracts),
	}
	in := Context{SessionDir: dir}
	for _, segment := range []string{"04:10", "04:40-04:10", "junk-junk"} {
		if _, err := tool.Execute(context.Background(), videoSegmentArgs(t, name, segment, 0, false), in); err == nil {
			t.Errorf("segment %q was accepted, want an error", segment)
		}
	}
	if _, err := tool.Execute(context.Background(),
		videoSegmentArgs(t, name, "04:10-04:40", analyzeVideoMaxFocusFrames+1, false), in); err == nil {
		t.Errorf("segment_frames %d was accepted, want an error", analyzeVideoMaxFocusFrames+1)
	}
	if runs != 0 || extracts != 0 {
		t.Fatalf("a rejected segment still did work: ran %d, extracted %d", runs, extracts)
	}
}

// The listing says which window was sampled more densely, so a timeline with
// bunched timestamps is explained rather than mysterious.
func TestAnalyzeVideoFrameListingFocusNote(t *testing.T) {
	m := &VideoManifest{
		Kept:             4,
		Sampled:          60,
		DuplicateDropped: 50,
		BlurDropped:      6,
		SegmentStartSec:  250,
		SegmentEndSec:    280,
		FocusFPS:         0.7,
		FocusKept:        20,
		Frames: []VideoFrameInfo{{
			Label:  "[00:04:10]",
			Path:   filepath.Join(t.TempDir(), "001_00m04s10.jpg"),
			Change: 0.2,
		}},
	}
	out := videoFrameListing(m, t.TempDir())
	if !strings.Contains(out, "Focused 00:04:10–00:04:40: 20 frames sampled inside that window at 0.7 fps.") {
		t.Fatalf("listing = %q, want the focus note", out)
	}
}
