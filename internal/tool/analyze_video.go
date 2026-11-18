package tool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// AnalyzeVideoFunc runs the chunked vision pipeline over extracted keyframes
// and returns the timeline text. Implemented by agent.LoopRunner.RunVideoSession
// and wired in from server.go and cli/run.go, the way AnalyzeImageFunc is, so
// the tool package never imports the agent package.
type AnalyzeVideoFunc func(ctx context.Context, req VideoAnalysisRequest) (string, error)

// VideoFrame is one keyframe on its way to the vision model: the JPEG payload
// plus the timestamp label the model reads alongside the image. Labels ride the
// message text in the same order as the images — never burned into the pixels,
// which would cost tokens twice and corrupt any later OCR of the frame.
type VideoFrame struct {
	Label     string // "[00:02:03]"
	MediaType string // "image/jpeg"
	Base64    string // base64-encoded JPEG bytes
}

// VideoAnalysisRequest is everything one video analysis needs, assembled by the
// tool and carried across the package boundary as a value.
type VideoAnalysisRequest struct {
	// Title is the recording's file name, for the prompt's opening line.
	Title string
	// Duration is the recording's length as "00:12:34".
	Duration string
	// Mode is describe, transcribe or answer.
	Mode string
	// Question is set only for mode answer.
	Question string
	// Segment is the focus window as "00:04:10–00:04:40" when the caller
	// asked for denser sampling inside one stretch, empty for uniform
	// sampling.
	Segment string
	// Frames are the kept keyframes, in chronological order.
	Frames []VideoFrame
	// Transcript carries "[00:01:12] text" lines when the recording had a
	// sidecar or embedded subtitle track, so speech and screen are aligned.
	Transcript []string
	// Model, ProviderID and Effort are the already-resolved vision settings.
	Model      string
	ProviderID string
	Effort     string
}

// analyzeVideoTimeout bounds one whole video analysis: extraction plus one
// vision call per frame chunk plus the merge. A ten-minute recording is a dozen
// calls, so a single image's two-minute budget is far too tight.
const analyzeVideoTimeout = 10 * time.Minute

// analyzeVideoMaxBytes caps the video the tool will read. Screen recordings run
// large, but a file past this size is a mis-aimed call rather than a video the
// model can help with.
const analyzeVideoMaxBytes = 2 << 30

// Defaults for the extractor's sampling knobs.
const (
	analyzeVideoDefaultFrames = 40
	analyzeVideoDefaultFPS    = 1.0
)

// A focus window adds a second, denser pass inside it on top of the overview
// budget, so the detail frames never eat the whole-recording coverage.
const (
	analyzeVideoDefaultFocusFrames = 20
	analyzeVideoMaxFocusFrames     = 60
)

// The three analysis modes a recording can be asked for.
const (
	analyzeVideoModeDescribe   = "describe"
	analyzeVideoModeTranscribe = "transcribe"
	analyzeVideoModeAnswer     = "answer"
)

// AnalyzeVideoTool reads a screen recording through a vision-capable model:
// ffmpeg extracts the frames that actually changed, local dedup drops repeats,
// and the survivors go to the model in timestamped chunks, merged into one
// timeline. It exists because a session's own model almost never accepts video,
// and because sending every frame wastes both money and attention.
type AnalyzeVideoTool struct {
	Run AnalyzeVideoFunc
	// Extract, when set, replaces the ffmpeg extractor. Tests drive the tool
	// with a prepared manifest instead of decoding a real recording; nil uses
	// ExtractVideoFrames.
	Extract func(ctx context.Context, opts VideoExtractOptions) (*VideoManifest, error)
	// ResolveModel, when set, maps the session's inherited (model, provider)
	// to the model that will run the vision calls (the settings-screen image
	// model override), so cache entries key on the resolved model.
	ResolveModel func(inheritedModel, inheritedProvider string) (model, providerID string)
	// Cache stores the finished timeline keyed by the recording's identity,
	// mode, question and resolved model. nil disables caching.
	Cache *webcache.Cache
}

func (AnalyzeVideoTool) ID() string { return "analyze_video" }

func (AnalyzeVideoTool) Description() string {
	return "Describe, transcribe or answer questions about a screen recording — an .mp4, .mov, .m4v, .webm, .mkv or .avi file of a screen, slides, a demo, a tutorial or any screen-capture footage — using a vision-capable model. Extraction runs locally: ffmpeg samples the video once per second, frames whose on-screen content actually changed are kept (a moving cursor is ignored), duplicates and blank transitions are dropped, and only those keyframes reach the model, in timestamped chunks merged into one timeline. Pass segment (for example \"04:10-04:40\") to keep the whole recording covered at the normal rate while sampling inside that window much more densely — up to segment_frames extra frames, default 20 — so a dialog, menu or error that lives for a second or two is not lost between samples. Requires ffmpeg on PATH. mode describe (the default) returns a timestamped account of what happens on screen, transcribe returns the text and UI state visible at each timestamp, and answer answers a question about the recording. A subtitle file next to the video (.srt/.vtt) or an embedded subtitle track is aligned by timestamp and given to the model as text. The kept frames are written to a scratch directory and listed in the result, so a later view_image or analyze_image call can inspect any single one. Results are cached by the recording's identity, so re-analyzing an unchanged video from a later step is served from cache."
}

func (AnalyzeVideoTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the video file (absolute or relative to the session directory): mp4, mov, m4v, webm, mkv, avi"
			},
			"question": {
				"type": "string",
				"description": "For mode answer: what to ask about the recording. Ignored otherwise"
			},
			"mode": {
				"type": "string",
				"enum": ["describe", "transcribe", "answer"],
				"description": "What to return: a timestamped account of what happens on screen (describe, the default), the text and UI state visible at each timestamp (transcribe), or the answer to question (answer)"
			},
			"fps": {
				"type": "number",
				"description": "Frames sampled per second of video for change detection, default 1 — screen recordings change far slower than they play, and this is the diff rate, not the frame rate"
			},
			"max_frames": {
				"type": "integer",
				"description": "Cap on keyframes sent to the model, default 40 — past roughly this many images in one analysis, attention to any single frame drops"
			},
			"segment": {
				"type": "string",
				"description": "Focus window to sample much more densely than the rest of the recording, as \"04:10-04:40\" or \"00:04:10-00:04:40\" (plain seconds also accepted). The whole recording stays covered at the normal rate; omit for uniform sampling"
			},
			"segment_frames": {
				"type": "integer",
				"description": "Cap on the extra detail frames sampled inside segment, default 20, at most 60 — they come on top of max_frames"
			},
			"fresh": {
				"type": "boolean",
				"description": "Ignore any cached timeline and analyze the recording again — still refreshes the cache"
			},
			"effort": {
				"type": "string",
				"enum": ["low", "medium", "high"],
				"description": "Reasoning depth for the vision calls, overriding the session's own — low for a quick skim, high for a dense tutorial or a hard diagnosis. Omit to inherit the session choice; a level the vision model does not take is ignored"
			}
		},
		"required": ["path"]
	}`)
}

// analyzeVideoMetadata identifies a completed analysis, the way
// analyzeImageMetadata does. Cached tells a replayed timeline from a fresh one.
func analyzeVideoMetadata(path, mode string, cached bool, frames int) map[string]any {
	return map[string]any{
		"analyzeVideoPath":   path,
		"analyzeVideoMode":   mode,
		"analyzeVideoCached": cached,
		"analyzeVideoFrames": frames,
	}
}

// analyzeVideoCachedFooter marks a replay so the model knows the recording was
// not looked at again.
func analyzeVideoCachedFooter(base, mode string) string {
	return fmt.Sprintf("\n\n_Served the cached %s of %s; pass fresh=true to analyze the recording again._", mode, base)
}

func (t AnalyzeVideoTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path          string  `json:"path"`
		Question      string  `json:"question"`
		Mode          string  `json:"mode"`
		FPS           float64 `json:"fps"`
		MaxFrames     int     `json:"max_frames"`
		Segment       string  `json:"segment"`
		SegmentFrames int     `json:"segment_frames"`
		Fresh         bool    `json:"fresh"`
		Effort        string  `json:"effort"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Path == "" {
		return Result{Output: "path is required"}, nil
	}

	mode := input.Mode
	if mode == "" {
		mode = analyzeVideoModeDescribe
	}
	switch mode {
	case analyzeVideoModeDescribe, analyzeVideoModeTranscribe, analyzeVideoModeAnswer:
	default:
		return Result{}, fmt.Errorf("invalid mode %q (supported: describe, transcribe, answer)", mode)
	}
	// An answer with nothing to ask is a describe; a question offered to the
	// other modes is ignored, so the cache key describes the work actually
	// done.
	if mode == analyzeVideoModeAnswer && strings.TrimSpace(input.Question) == "" {
		mode = analyzeVideoModeDescribe
	}
	question := input.Question
	if mode != analyzeVideoModeAnswer {
		question = ""
	}

	effort := strings.TrimSpace(input.Effort)
	if effort != "" && !validImageEffort(effort) {
		return Result{}, fmt.Errorf("invalid effort %q (supported: low, medium, high)", effort)
	}
	if effort == "" {
		effort = tctx.Effort
	}

	path := resolveReadPath(tctx.SessionDir, input.Path)
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("read video %s: %w", path, err)
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("video %s is a directory", path)
	}
	if info.Size() > analyzeVideoMaxBytes {
		return Result{}, fmt.Errorf("video %s is %.0f MB — analyze_video handles at most %d GB",
			path, float64(info.Size())/(1<<20), analyzeVideoMaxBytes>>30)
	}
	if info.Size() == 0 {
		return Result{}, fmt.Errorf("video %s is empty", path)
	}

	model, providerID := tctx.Model, tctx.Provider
	if t.ResolveModel != nil {
		model, providerID = t.ResolveModel(model, providerID)
	}

	fps := input.FPS
	if fps <= 0 {
		fps = analyzeVideoDefaultFPS
	}
	maxFrames := input.MaxFrames
	if maxFrames <= 0 {
		maxFrames = analyzeVideoDefaultFrames
	}
	// A focus window buys its own dense pass on top of the overview budget;
	// without one the extractor samples the recording uniformly.
	segStart, segEnd, hasSegment, err := parseVideoSegment(input.Segment)
	if err != nil {
		return Result{}, err
	}
	segmentKey, segmentLabel := "", ""
	focusFrames := 0
	if hasSegment {
		segmentKey = fmt.Sprintf("%.3f-%.3f", segStart, segEnd)
		segmentLabel = videoStamp(segStart) + "–" + videoStamp(segEnd)
		focusFrames = input.SegmentFrames
		if focusFrames <= 0 {
			focusFrames = analyzeVideoDefaultFocusFrames
		}
		if focusFrames > analyzeVideoMaxFocusFrames {
			return Result{}, fmt.Errorf("segment_frames %d is too many (at most %d detail frames)", focusFrames, analyzeVideoMaxFocusFrames)
		}
	}

	// The recording's identity, not its bytes: hashing a multi-gigabyte file to
	// answer a cache question costs more than the lookup saves. Path, size and
	// modification time together catch a re-recorded or replaced file.
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d",
		path, info.Size(), info.ModTime().UnixNano()))))
	key := webcache.Key(webcache.KindVideo, identity, mode, question, model, providerID, effort,
		fmt.Sprint(maxFrames), fmt.Sprint(fps), segmentKey, fmt.Sprint(focusFrames))
	caching := t.Cache != nil && t.Cache.Enabled()
	base := filepath.Base(path)

	if caching && !input.Fresh {
		if body, ok := t.Cache.Get(webcache.KindVideo, key); ok && len(strings.TrimSpace(string(body))) > 0 {
			return Result{
				Title:    "Analyze video: " + base,
				Output:   string(body) + analyzeVideoCachedFooter(base, mode),
				Metadata: analyzeVideoMetadata(path, mode, true, 0),
			}, nil
		}
	}

	// The tag keeps one extraction's frames apart from another's: a call with a
	// different rate, cap or focus window must decode again rather than inherit
	// a manifest that describes a different sampling.
	workDir := filepath.Join(os.TempDir(), "ogcode-video", identity[:16], videoExtractTag(fps, maxFrames, focusFrames, segmentKey))
	manifest, reused := readVideoManifest(workDir, path)
	if !reused {
		extract := t.Extract
		if extract == nil {
			extract = ExtractVideoFrames
		}
		extractCtx, cancel := context.WithTimeout(ctx, analyzeVideoTimeout)
		defer cancel()
		manifest, err = extract(extractCtx, VideoExtractOptions{
			Path:         path,
			OutDir:       workDir,
			SampleFPS:    fps,
			MaxFrames:    maxFrames,
			SegmentStart: segStart,
			SegmentEnd:   segEnd,
			FocusFrames:  focusFrames,
		})
		if err != nil {
			return Result{Output: fmt.Sprintf("Video extraction error: %s", err)}, nil
		}
	}
	if manifest == nil || len(manifest.Frames) == 0 {
		return Result{Output: fmt.Sprintf("No frames could be extracted from %s — it may be shorter than one sample or have no video stream.", base)}, nil
	}

	frames := make([]VideoFrame, 0, len(manifest.Frames))
	for _, f := range manifest.Frames {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			slog.Warn("analyze_video: frame unreadable", "path", f.Path, "err", err)
			continue
		}
		frames = append(frames, VideoFrame{
			Label:     f.Label,
			MediaType: "image/jpeg",
			Base64:    base64.StdEncoding.EncodeToString(data),
		})
	}
	if len(frames) == 0 {
		return Result{Output: fmt.Sprintf("The keyframes extracted from %s are no longer on disk — run again with fresh=true.", base)}, nil
	}

	transcript := make([]string, 0, len(manifest.Transcript))
	for _, line := range manifest.Transcript {
		transcript = append(transcript, fmt.Sprintf("[%s] %s", line.Stamp, line.Text))
	}

	analyzeCtx, cancel := context.WithTimeout(ctx, analyzeVideoTimeout)
	defer cancel()
	text, err := t.Run(analyzeCtx, VideoAnalysisRequest{
		Title:      base,
		Duration:   videoStamp(manifest.DurationSec),
		Mode:       mode,
		Question:   question,
		Segment:    segmentLabel,
		Frames:     frames,
		Transcript: transcript,
		Model:      model,
		ProviderID: providerID,
		Effort:     effort,
	})
	if err != nil {
		return Result{Output: fmt.Sprintf("Video analysis error: %s", err)}, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{Output: fmt.Sprintf("The vision model returned nothing for %s.", base)}, nil
	}

	output := text + "\n\n" + videoFrameListing(manifest, tctx.SessionDir)

	if caching {
		if err := t.Cache.Put(webcache.KindVideo, key, []byte(output)); err != nil {
			slog.Warn("analyze_video: cache result", "err", err)
		}
	}

	return Result{
		Title:    "Analyze video: " + base,
		Output:   output,
		Metadata: analyzeVideoMetadata(path, mode, false, len(frames)),
	}, nil
}

// videoFrameListing renders the kept frames as a compact table: one line per
// frame with its label, path and change score. The paths are what a follow-up
// view_image or analyze_image call needs, so they ride the result rather than
// being left to a second extraction.
func videoFrameListing(m *VideoManifest, sessionDir string) string {
	var b strings.Builder
	b.WriteString("### Frames\n\n")
	fmt.Fprintf(&b, "_%d of %d sampled frames kept, %d dropped as unchanged, %d blurry%s. Each frame is on disk for a follow-up view_image or analyze_image call._\n\n",
		m.Kept, m.Sampled, m.DuplicateDropped, m.BlurDropped, omittedNote(m))
	if m.SegmentEndSec > 0 {
		fmt.Fprintf(&b, "_Focused %s–%s: %d frames sampled inside that window at %.1f fps._\n\n",
			videoStamp(m.SegmentStartSec), videoStamp(m.SegmentEndSec), m.FocusKept, m.FocusFPS)
	}
	if len(m.Transcript) > 0 {
		fmt.Fprintf(&b, "_Transcript: %d subtitle lines aligned by timestamp._\n\n", len(m.Transcript))
	}
	for _, f := range m.Frames {
		fmt.Fprintf(&b, "- %s %s (change %.1f%%)\n", f.Label, displayReadPath(sessionDir, f.Path), f.Change*100)
	}
	return strings.TrimRight(b.String(), "\n")
}

// omittedNote reports frames the cap dropped, so a capped timeline is never
// mistaken for a complete one.
func omittedNote(m *VideoManifest) string {
	if m.Omitted <= 0 {
		return ""
	}
	return fmt.Sprintf(", %d omitted past the frame cap", m.Omitted)
}

// parseVideoSegment parses the caller's focus window: "04:10-04:40",
// "00:04:10-00:04:40" or plain seconds. A blank segment means uniform sampling.
func parseVideoSegment(s string) (start, end float64, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, nil
	}
	left, right, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false, fmt.Errorf("invalid segment %q — give a start and an end, e.g. \"04:10-04:40\"", s)
	}
	start, err = videoClockSeconds(left)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid segment %q: %w", s, err)
	}
	end, err = videoClockSeconds(right)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid segment %q: %w", s, err)
	}
	if end <= start {
		return 0, 0, false, fmt.Errorf("invalid segment %q — the end must come after the start", s)
	}
	return start, end, true, nil
}

// videoClockSeconds reads one end of a segment: plain seconds, "mm:ss" or
// "hh:mm:ss", the fraction forms a subtitle timestamp carries included.
func videoClockSeconds(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("missing timestamp")
	}
	if !strings.Contains(s, ":") {
		sec, err := strconv.ParseFloat(s, 64)
		if err != nil || sec < 0 {
			return 0, fmt.Errorf("%q is not a timestamp", s)
		}
		return sec, nil
	}
	sec, ok := videoSubtitleSeconds(s)
	if !ok {
		return 0, fmt.Errorf("%q is not a timestamp", s)
	}
	return sec, nil
}

// videoExtractTag names one extraction's scratch directory, so a call with a
// different rate, cap or focus window decodes again instead of inheriting a
// manifest that describes another sampling.
func videoExtractTag(fps float64, maxFrames, focusFrames int, segment string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%g|%d|%d|%s", fps, maxFrames, focusFrames, segment)))
	return fmt.Sprintf("%x", sum[:6])
}

// readVideoManifest reloads a previous extraction for the same unchanged file,
// so a second call reuses the frames instead of decoding the video again.
func readVideoManifest(dir, source string) (*VideoManifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, false
	}
	var m VideoManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	if m.Source != source || len(m.Frames) == 0 {
		return nil, false
	}
	for _, f := range m.Frames {
		if _, err := os.Stat(f.Path); err != nil {
			return nil, false
		}
	}
	return &m, true
}

// Compile-time guard: AnalyzeVideoTool satisfies the ToolDef interface.
var _ ToolDef = AnalyzeVideoTool{}
