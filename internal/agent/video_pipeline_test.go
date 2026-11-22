package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// videoTestProvider scripts the video pipeline's provider and records EVERY
// request, because a chunked session makes several calls and each one's shape
// (image count, legend, budget) carries an assertion.
type videoTestProvider struct {
	models   []provider.ModelInfo
	events   []provider.StreamEvent
	requests []provider.StreamRequest
}

func (p *videoTestProvider) ID() string { return "visiontest" }

func (p *videoTestProvider) Models() []provider.ModelInfo { return p.models }

func (p *videoTestProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.requests = append(p.requests, req)
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, evt := range p.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (p *videoTestProvider) EffortSpec(model string) provider.EffortSpec {
	for _, m := range p.models {
		if m.ID == model && len(m.Efforts) > 0 {
			return provider.EffortSpec{Levels: m.Efforts, Default: m.DefaultEffort}
		}
	}
	return provider.EffortSpec{}
}

// videoRunner wires a LoopRunner against the recording provider, mirroring the
// vision pipeline tests' harness: the provider MUST list every model a test
// routes to, because the pipeline resolves providers and checks image support
// through the registry.
func videoRunner(t *testing.T, models []provider.ModelInfo, events []provider.StreamEvent) (*LoopRunner, *videoTestProvider) {
	t.Helper()
	p := &videoTestProvider{
		models: models,
		events: events,
	}
	reg := provider.NewRegistry()
	reg.Register(p)
	return &LoopRunner{Registry: reg, DefaultProvider: p}, p
}

// videoTestModel is the vision-capable model every pin routes to.
func videoTestModel() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}}
}

// videoTestFrames builds n frames labelled one second apart, so timestamps and
// transcript windows come out deterministic: frame i carries [00:00:i].
func videoTestFrames(n int) []tool.VideoFrame {
	frames := make([]tool.VideoFrame, n)
	for i := range frames {
		frames[i] = tool.VideoFrame{
			Label:     fmt.Sprintf("[00:00:%02d]", i),
			MediaType: "image/jpeg",
			Base64:    fmt.Sprintf("frame-%02d", i),
		}
	}
	return frames
}

// videoUserText unpacks a request's user content (a JSON-marshalled string —
// the shape oneShotVisionLLM sends) and checks there is exactly one user
// message on the request.
func videoUserText(t *testing.T, req provider.StreamRequest) string {
	t.Helper()
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("request carries %d messages, want exactly one user message", len(req.Messages))
	}
	var text string
	if err := json.Unmarshal(req.Messages[0].Content, &text); err != nil {
		t.Fatalf("user content is not JSON text: %v", err)
	}
	return text
}

// TestVideoPipelineChunksTwentyFiveFrames pins the map/reduce shape: 25
// frames at ten images per chunk make THREE chunk calls (10, 10 and 5 images)
// plus one text-only merge call, the image order matches the frames' label
// order, and the legend names every chunk label in order.
func TestVideoPipelineChunksTwentyFiveFrames(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "chunk note"}})

	out, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Title:      "demo.mp4",
		Duration:   "00:03:00",
		Mode:       visionModeDescribe,
		Frames:     videoTestFrames(25),
		Model:      "eye-model",
		ProviderID: "visiontest",
	})
	if err != nil {
		t.Fatalf("RunVideoSession: %v", err)
	}

	if got := len(p.requests); got != 4 {
		t.Fatalf("provider saw %d calls, want 4 (three chunks + one reduce): %s", got, out)
	}
	for i, wantImages := range []int{10, 10, 5, 0} {
		if got := len(p.requests[i].Messages[0].Images); got != wantImages {
			t.Errorf("chunk %d carries %d images, want %d", i+1, got, wantImages)
		}
	}
	// The merge call is the text-only one: no images, the reduce prompt and
	// budget.
	if sys := p.requests[3].System; len(sys) != 1 || sys[0] != videoReduceSystem {
		t.Errorf("reduce System = %v, want the merge prompt", sys)
	}
	if want := videoReduceMaxTokens + visionThinkingHeadroom; p.requests[3].MaxTokens != want {
		t.Errorf("reduce MaxTokens = %d, want %d (budget plus thinking headroom)", p.requests[3].MaxTokens, want)
	}
	if want := videoChunkMaxTokens + visionThinkingHeadroom; p.requests[0].MaxTokens != want {
		t.Errorf("chunk MaxTokens = %d, want %d (budget plus thinking headroom)", p.requests[0].MaxTokens, want)
	}

	// Image order follows the frames' label order, chunk by chunk.
	for _, tc := range []struct {
		call  int
		first int
	}{
		{0, 0}, {1, 10}, {2, 20},
	} {
		for i, img := range p.requests[tc.call].Messages[0].Images {
			if want := fmt.Sprintf("frame-%02d", tc.first+i); img.Data != want {
				t.Errorf("chunk %d image %d = %q, want %q", tc.call+1, i, img.Data, want)
			}
		}
	}

	// The legend lists every label of the chunk, in order.
	for _, tc := range []struct {
		call   int
		first  int
		labels string
	}{
		{0, 0, "[00:00:00], [00:00:01], [00:00:02], [00:00:03], [00:00:04], [00:00:05], [00:00:06], [00:00:07], [00:00:08], [00:00:09]"},
		{1, 10, "[00:00:10], [00:00:11], [00:00:12], [00:00:13], [00:00:14], [00:00:15], [00:00:16], [00:00:17], [00:00:18], [00:00:19]"},
		{2, 20, "[00:00:20], [00:00:21], [00:00:22], [00:00:23], [00:00:24]"},
	} {
		text := videoUserText(t, p.requests[tc.call])
		if !strings.Contains(text, "Images follow in this order: "+tc.labels) {
			t.Errorf("call %d user text = %q, want the ordered legend", tc.call+1, text)
		}
	}
	firstText := videoUserText(t, p.requests[0])
	if !strings.Contains(firstText, "frames 1–10 of 25") {
		t.Errorf("first chunk user text = %q, want its position in the recording", firstText)
	}
	if !strings.Contains(firstText, "Recording: demo.mp4") || !strings.Contains(firstText, "duration 00:03:00") {
		t.Errorf("first chunk user text = %q, want the recording's identity", firstText)
	}
}

// TestVideoPipelineTwentyFramesChunksThenReduce pins the shape at the exact
// multiple: 20 frames make exactly three provider calls — two chunk calls of
// ten images each and a third text-only merge call carrying none.
func TestVideoPipelineTwentyFramesChunksThenReduce(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "chunk note"}})

	out, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:       visionModeDescribe,
		Frames:     videoTestFrames(20),
		Model:      "eye-model",
		ProviderID: "visiontest",
	})
	if err != nil {
		t.Fatalf("RunVideoSession: %v", err)
	}
	if got := len(p.requests); got != 3 {
		t.Fatalf("provider saw %d calls, want 3 (two chunk calls + one reduce): %s", got, out)
	}
	for i, wantImages := range []int{10, 10, 0} {
		if got := len(p.requests[i].Messages[0].Images); got != wantImages {
			t.Errorf("call %d carries %d images, want %d", i+1, got, wantImages)
		}
	}
	if !strings.Contains(out, "chunk note") {
		t.Errorf("out = %q, want the merged note", out)
	}
}

// TestVideoPipelineTranscriptWindowFiltersPerChunk pins the alignment: a
// transcript line appears only in the chunk whose time window its
// "[hh:mm:ss]" prefix falls inside, never in two chunks, and lines without a
// parsable prefix are dropped.
func TestVideoPipelineTranscriptWindowFiltersPerChunk(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "note"}})

	// Twelve frames one second apart: chunk 1 holds frames [00:00:00]–
	// [00:00:09] and chunk 2 [00:00:10]–[00:00:11], whose window extends to
	// the end of the recording.
	_, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:   visionModeDescribe,
		Frames: videoTestFrames(12),
		Transcript: []string{
			"[00:00:05] early words",
			"[00:00:21] later words",
			"[00:01:00] after the last frame",
			"no timestamp here",
		},
		Model:      "eye-model",
		ProviderID: "visiontest",
	})
	if err != nil {
		t.Fatalf("RunVideoSession: %v", err)
	}
	if len(p.requests) != 3 {
		t.Fatalf("provider saw %d calls, want 3 (two chunk calls + one merge)", len(p.requests))
	}
	if got := len(p.requests[2].Messages[0].Images); got != 0 {
		t.Errorf("merge call carries %d images, want none", got)
	}

	first := videoUserText(t, p.requests[0])
	second := videoUserText(t, p.requests[1])
	if !strings.Contains(first, "Spoken at these moments:\n[00:00:05] early words") {
		t.Errorf("chunk 1 text %q, want the line inside its window", first)
	}
	if strings.Contains(first, "later words") || strings.Contains(first, "after the last frame") {
		t.Errorf("chunk 1 text %q, want lines outside its window dropped", first)
	}
	if !strings.Contains(second, "[00:00:21] later words") || !strings.Contains(second, "after the last frame") {
		t.Errorf("chunk 2 text %q, want the last chunk's extended window", second)
	}
	if strings.Contains(second, "early words") {
		t.Errorf("chunk 2 text %q, want the early line only in chunk 1", second)
	}
	if strings.Count(first+second, "later words") > 1 {
		t.Error("a transcript line appeared in more than one chunk")
	}
	if strings.Contains(second, "no timestamp here") || strings.Contains(first, "no timestamp here") {
		t.Error("a line without a parsable [hh:mm:ss] prefix leaked into a prompt")
	}
}

// TestVideoPipelineSingleChunkAddsNoReduce pins the boundary: a recording of
// at most ten frames is one vision call, returned unchanged — no merge call.
func TestVideoPipelineSingleChunkAddsNoReduce(t *testing.T) {
	for _, n := range []int{1, 10} {
		lr, p := videoRunner(t, videoTestModel(),
			[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "the only note"}})

		out, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
			Mode:       visionModeDescribe,
			Frames:     videoTestFrames(n),
			Model:      "eye-model",
			ProviderID: "visiontest",
		})
		if err != nil {
			t.Fatalf("%d frames: RunVideoSession: %v", n, err)
		}
		if got := len(p.requests); got != 1 {
			t.Fatalf("%d frames: provider saw %d calls, want exactly 1", n, got)
		}
		if sys := p.requests[0].System; len(sys) != 1 || sys[0] != videoDescribeSystem {
			t.Errorf("%d frames: System = %v, want the describe prompt", n, sys)
		}
		want := "the only note\n\n_Analyzed via eye-model (visiontest) in"
		if !strings.HasPrefix(out, "the only note") || !strings.Contains(out, "_Analyzed via eye-model (visiontest) in") {
			t.Errorf("%d frames: out = %q, want the chunk text unchanged plus the footer %q", n, out, want)
		}
	}
}

// TestVideoPipelineAnswerBlankQuestionDegradesToDescribe pins the mode
// normalisation: unknown modes and a questionless answer behave as describe,
// no question restatement leaks into the chunk prompts, while a real question
// rides in every chunk's user text.
func TestVideoPipelineAnswerBlankQuestionDegradesToDescribe(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "note"}})

	for _, tc := range []struct{ name, mode, question string }{
		{"blank question", visionModeAnswer, "   "},
		{"unknown mode", "summarize", "why did it fail"},
	} {
		if _, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
			Mode:       tc.mode,
			Question:   tc.question,
			Frames:     videoTestFrames(3),
			Model:      "eye-model",
			ProviderID: "visiontest",
		}); err != nil {
			t.Fatalf("%s: RunVideoSession: %v", tc.name, err)
		}
		if len(p.requests) != 1 {
			continue
		}
		text := videoUserText(t, p.requests[0])
		if strings.Contains(text, "this question") || strings.Contains(text, tc.question) {
			t.Errorf("%s: user text %q, want no question text in the prompt", tc.name, text)
		}
		if !strings.Contains(text, "short bulleted note") {
			t.Errorf("%s: user text %q, want the describe directive", tc.name, text)
		}
		want := videoDescribeSystem
		if sys := p.requests[0].System; sys[0] != want {
			t.Errorf("%s: System = %v, want the describe prompt", tc.name, sys)
		}
		p.requests = nil
	}

	// A real question rides in the chunk's user text, verbatim.
	if _, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:       visionModeAnswer,
		Question:   "why did the build fail",
		Frames:     videoTestFrames(3),
		Model:      "eye-model",
		ProviderID: "visiontest",
	}); err != nil {
		t.Fatalf("RunVideoSession: %v", err)
	}
	text := videoUserText(t, p.requests[0])
	if !strings.Contains(text, "this question: why did the build fail") {
		t.Errorf("answer user text %q, want the question restated", text)
	}
}

// TestVideoPipelineTranscribeModeSendsItsOwnPrompt pins the one mode whose
// prompt differs from describe's.
func TestVideoPipelineTranscribeModeSendsItsOwnPrompt(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "note"}})

	if _, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:       visionModeTranscribe,
		Frames:     videoTestFrames(5),
		Model:      "eye-model",
		ProviderID: "visiontest",
	}); err != nil {
		t.Fatalf("RunVideoSession: %v", err)
	}
	if sys := p.requests[0].System; sys[0] != videoTranscribeSystem {
		t.Errorf("System = %v, want the transcribe prompt", sys)
	}
	if text := videoUserText(t, p.requests[0]); !strings.Contains(text, "Transcribe what is visible") {
		t.Errorf("user text %q, want the transcribe directive", text)
	}
}

// TestVideoPipelineEmptyFramesErrors pins the input gate: no frames is an
// error, and the provider is never consulted.
func TestVideoPipelineEmptyFramesErrors(t *testing.T) {
	lr, p := videoRunner(t, videoTestModel(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "note"}})

	_, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:       visionModeDescribe,
		Model:      "eye-model",
		ProviderID: "visiontest",
	})
	if err == nil {
		t.Fatal("RunVideoSession succeeded with no frames, want an error")
	}
	if !strings.Contains(err.Error(), "no frames") {
		t.Errorf("err = %v, want it to name the missing frames", err)
	}
	if len(p.requests) != 0 {
		t.Errorf("provider saw %d calls, want none", len(p.requests))
	}
}

// TestVideoPipelineRejectsNonVisionModel pins the vision gate on the new
// tool's text: a model that cannot see fails loudly with the settings path.
func TestVideoPipelineRejectsNonVisionModel(t *testing.T) {
	lr, p := videoRunner(t,
		[]provider.ModelInfo{{ID: "blind-model"}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

	_, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
		Mode:       visionModeDescribe,
		Frames:     videoTestFrames(3),
		Model:      "blind-model",
		ProviderID: "visiontest",
	})
	if err == nil {
		t.Fatal("RunVideoSession succeeded with a non-vision model, want an error")
	}
	if !strings.Contains(err.Error(), "analyze_video needs a vision-capable model") {
		t.Errorf("err = %v, want the analyze_video vision-capability message", err)
	}
	if len(p.requests) != 0 {
		t.Errorf("provider saw %d calls, want none", len(p.requests))
	}
}

// TestVideoPipelineFocusWindowPrompt pins the note that tells the model when
// its samples are denser inside one stretch, so a compressed timeline is not
// read as the screen moving faster — and that a uniform sampling carries no
// such note.
func TestVideoPipelineFocusWindowPrompt(t *testing.T) {
	const window = "00:00:04–00:00:07"
	run := func(segment string) *videoTestProvider {
		t.Helper()
		lr, p := videoRunner(t, videoTestModel(),
			[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "chunk note"}})
		if _, err := lr.RunVideoSession(context.Background(), tool.VideoAnalysisRequest{
			Title:      "demo.mp4",
			Duration:   "00:00:12",
			Mode:       visionModeDescribe,
			Segment:    segment,
			Frames:     videoTestFrames(11),
			Model:      "eye-model",
			ProviderID: "visiontest",
		}); err != nil {
			t.Fatalf("RunVideoSession(segment %q): %v", segment, err)
		}
		return p
	}

	p := run(window)
	if got := len(p.requests); got != 3 {
		t.Fatalf("provider saw %d calls, want 3 (two chunks + one reduce)", got)
	}
	chunk := videoUserText(t, p.requests[0])
	if !strings.Contains(chunk, "focus window "+window) {
		t.Errorf("chunk user text = %q, want the focus window named", chunk)
	}
	reduce := videoUserText(t, p.requests[2])
	if !strings.Contains(reduce, "Sampling inside "+window+" was denser than in the rest of the recording.") {
		t.Errorf("reduce user text = %q, want the denser-sampling note", reduce)
	}

	p = run("")
	if chunk := videoUserText(t, p.requests[0]); strings.Contains(chunk, "focus window") {
		t.Errorf("uniform sampling carried a focus note: %q", chunk)
	}
	if reduce := videoUserText(t, p.requests[2]); strings.Contains(reduce, "was denser than") {
		t.Errorf("uniform sampling carried a denser-sampling note: %q", reduce)
	}
}
