package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// The video pipeline is the chunked counterpart of the vision pipeline: it
// reads a whole screen recording by sending its extracted keyframes to the
// model a few at a time (one call per chunk, every image of the chunk on ONE
// user message) and, when more than one chunk ran, merging the chunk notes
// with a single text-only call. It is called by tool.AnalyzeVideoTool via the
// tool.AnalyzeVideoFunc contract.

// videoChunkFrames is how many keyframes one vision call carries. Both
// providers render the images of a message as one content array AFTER the text
// part, so the per-frame labels cannot travel interleaved — they ride the
// message text as an ordered legend instead. Ten frames keeps the payload of a
// call near what one analyze_image call carries while limiting how many vision
// calls a recording needs.
const videoChunkFrames = 10

// Output budgets. Each chunk is described with the same budget one image gets
// in the vision pipeline; the merge reads every chunk note at once, so it gets
// room for the whole timeline.
const (
	videoChunkMaxTokens  = visionDescribeMaxTokens
	videoReduceMaxTokens = 8192
)

// One fixed system prompt per mode, mirroring the vision pipeline's fixed
// prompts: the tool is deterministic, so the asking agent's framing never
// reaches the model. Answer mode reuses the describe prompt — a useful answer
// still comes out of what the frames show — and carries its question in each
// chunk's user text instead.
const videoDescribeSystem = `You read screen recordings frame by frame. For each labelled frame, say what the user did and what the screen shows at that moment, referencing the labels. Note screens, dialogs and state changes; do not describe the mouse pointer.`

const videoTranscribeSystem = `You read screen recordings. For each labelled frame, transcribe the visible text and name the application or screen state as precisely as the pixels allow; keep UI labels verbatim. Do not describe layout or colour.`

const videoReduceSystem = `You merge timestamped notes from one recording into a single timeline. Keep every distinct event and its label, drop repetition, and never invent a timestamp that is not in the notes.`

// RunVideoSession runs the chunked video-analysis pipeline and returns the
// model's timeline text. It is called by tool.AnalyzeVideoTool via the
// tool.AnalyzeVideoFunc contract.
//
// Unlike a free-form agent loop this is a deterministic pass: a fixed prompt
// per mode, one call per frame chunk, one merge call when more than one chunk
// ran. Frames arrive already encoded (label, media type and base64 JPEG); the
// labels must stay in the SAME order as the images on the wire, because the
// providers render a message's images after its text and in the order the
// slice carries.
func (lr *LoopRunner) RunVideoSession(ctx context.Context, req tool.VideoAnalysisRequest) (string, error) {
	if len(req.Frames) == 0 {
		return "", fmt.Errorf("video analysis: no frames provided")
	}

	// Normalise the mode the way the tool defaults it: anything blank or
	// unknown is a description. An answer with nothing to answer degrades to
	// a description too — still a useful, deterministic extraction rather
	// than an error.
	mode := req.Mode
	switch mode {
	case visionModeTranscribe, visionModeDescribe, visionModeAnswer:
	default:
		mode = visionModeDescribe
	}
	question := req.Question
	if mode == visionModeAnswer && strings.TrimSpace(question) == "" {
		mode = visionModeDescribe
	}
	// describe and transcribe ignore the question entirely.
	if mode != visionModeAnswer {
		question = ""
	}

	// The settings-screen image model, when set and available, wins; the
	// vision capability is mandatory, never a silent fallback — exactly as
	// the single-image pipeline resolves its call.
	model, providerID := lr.resolveVisionModel(req.Model, req.ProviderID)

	p, model, err := lr.resolveVisionProvider(model, providerID)
	if err != nil {
		return "", err
	}

	supportsImages := lr.Registry == nil || lr.Registry.ModelSupportsImages(model)
	if !supportsImages && lr.Store != nil {
		supportsImages = lr.resolveImageSupport(ctx, p, model)
	}
	if !supportsImages {
		return "", fmt.Errorf("model %q cannot accept images — analyze_video needs a vision-capable model. Pick one in Settings → General → Background tasks (\"Image analysis model\") and try again.", model)
	}

	// The effort is resolved against the RESOLVED vision model, not the
	// session's, and is a property of every call the session makes.
	effort := provider.ResolveEffort(p, model, req.Effort)

	started := time.Now()
	chunks := (len(req.Frames) + videoChunkFrames - 1) / videoChunkFrames
	notes := make([]string, 0, chunks)
	for start := 0; start < len(req.Frames); start += videoChunkFrames {
		end := start + videoChunkFrames
		if end > len(req.Frames) {
			end = len(req.Frames)
		}
		chunk := req.Frames[start:end]

		transcript := videoTranscriptWindow(req.Transcript, req.Frames, start)
		user := videoChunkUserText(req, mode, question, chunk, start, len(req.Frames), transcript)
		images := make([]provider.MessageImage, len(chunk))
		for i, f := range chunk {
			images[i] = provider.MessageImage{MediaType: f.MediaType, Data: f.Base64}
		}

		out, usage, err := oneShotVisionLLMMulti(ctx, p, model, videoSystemFor(mode), user, images, videoChunkMaxTokens, effort)
		lr.chargeUsage(ctx, providerIDOf(p), model, usage)
		if err != nil {
			return "", fmt.Errorf("video analysis: chunk %d of %d: %w", start/videoChunkFrames+1, chunks, err)
		}
		out = strings.TrimSpace(out)
		if out == "" {
			return "", fmt.Errorf("video analysis: chunk %d of %d: model %q returned no text for %d frames", start/videoChunkFrames+1, chunks, model, len(chunk))
		}
		notes = append(notes, out)
	}

	out := notes[0]
	if len(notes) > 1 {
		reduceUser := videoReduceUserText(req, mode, question, notes)
		text, usage, err := oneShotVisionLLMMulti(ctx, p, model, videoReduceSystem, reduceUser, nil, videoReduceMaxTokens, effort)
		lr.chargeUsage(ctx, providerIDOf(p), model, usage)
		if err != nil {
			return "", fmt.Errorf("video analysis: merge of %d chunk notes: %w", len(notes), err)
		}
		out = strings.TrimSpace(text)
		if out == "" {
			return "", fmt.Errorf("video analysis: merge: model %q returned no text for the timeline", model)
		}
	}

	elapsed := time.Since(started)
	slog.Info("video analysis complete", "mode", mode, "model", model, "frames", len(req.Frames), "chunks", len(notes), "chars", len(out), "duration", elapsed.Round(time.Millisecond))

	// The resolved model names itself in the returned text, as the
	// single-image pipeline's footer does.
	return out + fmt.Sprintf("\n\n_Analyzed via %s (%s) in %.1fs._", model, providerIDOf(p), elapsed.Seconds()), nil
}

// videoSystemFor returns the fixed system prompt for a normalized mode.
func videoSystemFor(mode string) string {
	switch mode {
	case visionModeTranscribe:
		return videoTranscribeSystem
	default: // visionModeDescribe and visionModeAnswer
		return videoDescribeSystem
	}
}

// videoTranscriptTimestamp parses the leading "[hh:mm:ss]" prefix of a
// transcript line or frame label and returns the seconds it names, with
// ok=false when the line carries no parsable prefix.
func videoTranscriptTimestamp(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "[") {
		return 0, false
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return 0, false
	}
	seconds := 0
	for _, part := range strings.Split(line[1:end], ":") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return 0, false
		}
		seconds = seconds*60 + n
	}
	return seconds, true
}

// videoTranscriptWindow keeps the transcript lines whose "[hh:mm:ss]" prefix
// falls inside one chunk's time window: from its first frame's timestamp
// through the next chunk's first frame (a line at that boundary belongs to the
// next chunk; the last chunk runs to the end of the recording). Lines without
// a parsable prefix and lines outside any window are dropped.
func videoTranscriptWindow(transcript []string, frames []tool.VideoFrame, start int) []string {
	startSecs, ok := videoTranscriptTimestamp(frames[start].Label)
	if !ok {
		return nil
	}
	endSecs := -1
	if start+videoChunkFrames < len(frames) {
		if s, ok := videoTranscriptTimestamp(frames[start+videoChunkFrames].Label); ok {
			endSecs = s
		}
	}
	kept := make([]string, 0, len(transcript))
	for _, line := range transcript {
		secs, ok := videoTranscriptTimestamp(line)
		if !ok || secs < startSecs || (endSecs >= 0 && secs >= endSecs) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// videoChunkUserText builds the ONE user text a chunk's message carries. The
// wire renders text before the images and cannot interleave, so the frame
// labels travel here as an ordered legend, with the recording's identity, the
// chunk's position, any transcript lines inside the chunk's window, and the
// mode's instruction after them.
func videoChunkUserText(req tool.VideoAnalysisRequest, mode, question string, chunk []tool.VideoFrame, start, total int, transcript []string) string {
	var b strings.Builder
	if title := strings.TrimSpace(req.Title); title != "" {
		fmt.Fprintf(&b, "Recording: %s", title)
	} else {
		b.WriteString("Recording: (untitled)")
	}
	if d := strings.TrimSpace(req.Duration); d != "" {
		fmt.Fprintf(&b, " — duration %s", d)
	}
	fmt.Fprintf(&b, ". You are given frames %d–%d of %d.\n", start+1, start+len(chunk), total)
	if seg := strings.TrimSpace(req.Segment); seg != "" {
		fmt.Fprintf(&b, "Sampling is denser inside the recording's focus window %s: frames there sit closer together in time and show that stretch in more detail than the rest — it is not played faster.\n", seg)
	}

	labels := make([]string, len(chunk))
	for i, f := range chunk {
		labels[i] = f.Label
	}
	fmt.Fprintf(&b, "Images follow in this order: %s.\n", strings.Join(labels, ", "))

	if len(transcript) > 0 {
		b.WriteString("\nSpoken at these moments:\n")
		for _, line := range transcript {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	switch mode {
	case visionModeTranscribe:
		b.WriteString("Transcribe what is visible at each labelled frame, as one short bulleted note keyed by its label.")
	case visionModeAnswer:
		fmt.Fprintf(&b, "Answer what you can from these frames toward this question: %s\n\nWrite your notes as one short bulleted note per frame, keyed by its label.", strings.TrimSpace(question))
	default: // visionModeDescribe
		b.WriteString("Write your notes as one short bulleted note per frame, keyed by its label, saying what the user did and what the screen shows.")
	}
	return b.String()
}

// videoReduceUserText builds the text-only merge call's message: the chunk
// notes in order, and the question restated when the mode is answer.
func videoReduceUserText(req tool.VideoAnalysisRequest, mode, question string, notes []string) string {
	var b strings.Builder
	if title := strings.TrimSpace(req.Title); title != "" {
		fmt.Fprintf(&b, "Recording: %s.\n", title)
	}
	if seg := strings.TrimSpace(req.Segment); seg != "" {
		fmt.Fprintf(&b, "Sampling inside %s was denser than in the rest of the recording.\n", seg)
	}
	for i, note := range notes {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Chunk %d notes:\n%s\n", i+1, note)
	}
	if mode == visionModeAnswer {
		fmt.Fprintf(&b, "\nAnswer this question about the recording: %s\n", strings.TrimSpace(question))
	}
	b.WriteString("\nMerge these notes into one chronological timeline that keeps the [hh:mm:ss] labels.")
	return b.String()
}

// oneShotVisionLLMMulti makes a video-analysis call carrying any number of
// images on one user message and returns its text and usage. The streaming
// loop is copied from oneShotVisionLLM rather than shared: that helper takes
// exactly one image by value and widening its signature would touch
// vision_pipeline.go, which carries an unrelated in-flight change.
//
// The resolved effort is also the call's declared difficulty, exactly as the
// single-image helper declares it.
func oneShotVisionLLMMulti(ctx context.Context, p provider.Provider, model, system, user string, images []provider.MessageImage, maxTokens int, effort string) (string, *provider.TokenUsage, error) {
	budget := maxTokens
	// Thinking is asked for only when a level was named, exactly as the
	// single-image helper does.
	thinking := effort != ""
	if thinking {
		budget += visionThinkingHeadroom
	}
	userJSON, err := json.Marshal(user)
	if err != nil {
		return "", nil, err
	}
	req := provider.StreamRequest{
		Model:  model,
		System: []string{system},
		Messages: []provider.ModelMessage{{
			Role:    "user",
			Content: userJSON,
			Images:  images,
		}},
		MaxTokens: budget,
		Thinking:  thinking,
		Effort:    effort,
		Task:      provider.TaskVision,
		// The agent's per-call choice is the routing label too, exactly as
		// the single-image helper carries it.
		Complexity: provider.ComplexityForEffort(effort),
	}
	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		return "", nil, err
	}
	var text, reasoning strings.Builder
	var streamErr, finish string
	var usage *provider.TokenUsage
	for evt := range ch {
		switch evt.Type {
		case provider.EventTextDelta:
			text.WriteString(evt.Text)
		case provider.EventReasoning:
			reasoning.WriteString(evt.Text)
		case provider.EventUsage:
			// Providers may repeat cumulative usage; the last report wins.
			usage = evt.Usage
		case provider.EventError:
			streamErr = evt.Error
		case provider.EventFinish:
			if evt.FinishReason != nil {
				finish = *evt.FinishReason
			}
		}
	}
	// Same failure rule as the single-image helper: partial text before a
	// transport error is a failed call, not a truncated answer.
	if streamErr != "" {
		return "", usage, fmt.Errorf("%s", streamErr)
	}
	out := strings.TrimSpace(text.String())
	if out == "" && finish == "length" {
		return "", usage, fmt.Errorf("no answer within %d output tokens (finish=%s, reasoningChars=%d)",
			budget, finish, reasoning.Len())
	}
	if out == "" {
		out = strings.TrimSpace(reasoning.String())
	}
	return out, usage, nil
}
