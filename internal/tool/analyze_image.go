package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"  // register BMP decoder
	_ "golang.org/x/image/webp" // register WebP decoder

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// AnalyzeImageFunc runs the one-shot image-analysis pipeline and returns the
// extracted text. Implemented by agent.LoopRunner.RunVisionSession and wired in
// from server.go to avoid the tool→agent import cycle, exactly as DeepSearchFunc
// is. model and providerID carry the settings-screen override and are empty when
// the user chose to inherit; the tool's schema never exposes them, so the
// pipeline resolves them (override → active session model) on every call.
// effort is the reasoning depth for the call — the caller's own choice when it
// names one, otherwise the run's inherited effort; "" runs the model at its
// default. A level the resolved vision model does not take is dropped by the
// pipeline.
type AnalyzeImageFunc func(ctx context.Context, imageBytes []byte, mediaType, mode, question, model, providerID, effort string) (string, error)

// analyzeImageTimeout bounds one image-analysis call. A single LLM call on a
// large image runs 10-30s on typical providers; slow local models can push
// past a minute, so 120s is generous without ever hanging a turn forever.
const analyzeImageTimeout = 120 * time.Second

// analyzeImageMaxBytes caps the file the tool reads: a twenty-megabyte image
// would decode into a payload no vision model accepts anyway, and a hard
// refusal here is clearer than an encode failure.
const analyzeImageMaxBytes = 20 << 20

// analyzeImagePassthroughBytes is the size under which an already-encoded JPEG
// or PNG is sent through byte-for-byte: re-encoding always costs a little OCR
// fidelity (text edges soften), so a payload the model can already take is
// preserved exactly.
const analyzeImagePassthroughBytes = 4 << 20

// The three analysis modes an image can be asked for.
const (
	analyzeImageModeTranscribe = "transcribe"
	analyzeImageModeDescribe   = "describe"
	analyzeImageModeAnswer     = "answer"
)

// AnalyzeImageTool lets an agent extract text or an answer from an image file
// with a vision-capable model, one fixed LLM call per invocation — for sessions
// whose own model cannot see images at all, and for long sessions that would
// otherwise re-send the same image every step. The extracted text is cached on
// the image's content hash, so a re-read of the same file is served from disk.
type AnalyzeImageTool struct {
	Run AnalyzeImageFunc
	// ResolveModel, when set, maps the session's inherited (model, provider)
	// to the model that will actually run the vision call (e.g. the
	// settings-screen image-analysis override). Used so cache entries are
	// keyed by the resolved model rather than the inherited session model.
	ResolveModel func(inheritedModel, inheritedProvider string) (model, providerID string)
	// Cache stores the extracted text keyed by the image's content hash, the
	// mode and the question: a cache hit costs no LLM round trip at all.
	// nil disables caching entirely.
	Cache *webcache.Cache
}

func (AnalyzeImageTool) ID() string { return "analyze_image" }

func (AnalyzeImageTool) Description() string {
	return "Extract text, an answer or a diagnosis from an image file using a vision-capable model. Use this when a PNG, JPEG, GIF, BMP or WebP image on disk needs to be read as text — a screenshot, a scanned page, a photo of a document, a diagram — and when your own model cannot see images, or when the same image would otherwise be sent to a vision model repeatedly. One fixed LLM call: mode transcribe returns the exact on-image text, mode describe returns what the image shows, mode answer answers a question about it — and a debugging question about a screenshot, error dialog, stack trace, terminal or rendered interface gets the visible evidence, what it indicates and what to check next. Pass effort to set the reasoning depth for this one analysis — low, medium or high — without moving the whole session: high for a dense page or a hard failure, low for a plain caption. Results are cached by the image's content hash, so re-analyzing an unchanged image from a later step is served from cache."
}

func (AnalyzeImageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the image file (absolute or relative to session directory)"
			},
			"question": {
				"type": "string",
				"description": "For mode answer: what to ask about the image — a question about what it contains, or a debugging question about the failure it shows. Ignored otherwise"
			},
			"mode": {
				"type": "string",
				"enum": ["transcribe", "describe", "answer"],
				"description": "What to extract: the exact on-image text (transcribe), a thorough description of what the image shows (describe), or the answer to question from what is visible (answer, the default — a debugging question is answered as evidence, reading and next step)"
			},
			"fresh": {
				"type": "boolean",
				"description": "Ignore any cached result and run the analysis again — still refreshes the cache"
			},
			"effort": {
				"type": "string",
				"enum": ["low", "medium", "high"],
				"description": "Reasoning depth for this analysis, overriding the session's own — low for a plain caption, medium for a page with structure to follow, high for a dense page or a hard diagnosis. Omit to inherit the session choice; a level the vision model does not take is ignored"
			}
		},
		"required": ["path"]
	}`)
}

// analyzeImageMetadata identifies a completed analysis, the way deep_search's
// marker does. Cached tells a replayed answer from a fresh one.
func analyzeImageMetadata(path, mode string, cached bool) map[string]any {
	return map[string]any{
		"analyzeImagePath":   path,
		"analyzeImageMode":   mode,
		"analyzeImageCached": cached,
	}
}

// analyzeImageCachedFooter renders the single trailing line a cached result
// carries, so an answer served from cache is never mistaken for a fresh one,
// matching the deep-search tool's convention.
func analyzeImageCachedFooter(base string, mode string) string {
	return "\n\n_Served the cached " + mode + " for " + base + "; pass fresh=true to analyze again._"
}

// validImageEffort reports whether level is one of the three depths the tool's
// schema offers (low, medium, high). The check is the tool's own, matching its
// enum: anything else is rejected here, before the call is made.
func validImageEffort(level string) bool {
	switch level {
	case provider.EffortLow, provider.EffortMedium, provider.EffortHigh:
		return true
	}
	return false
}

func (t AnalyzeImageTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path     string `json:"path"`
		Question string `json:"question"`
		Mode     string `json:"mode"`
		Fresh    bool   `json:"fresh"`
		Effort   string `json:"effort"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Path == "" {
		return Result{Output: "path is required"}, nil
	}

	mode := input.Mode
	if mode == "" {
		mode = analyzeImageModeAnswer
	}
	switch mode {
	case analyzeImageModeTranscribe, analyzeImageModeDescribe, analyzeImageModeAnswer:
	default:
		return Result{}, fmt.Errorf("invalid mode %q (supported: transcribe, describe, answer)", mode)
	}

	// Match the pipeline's own normalization (RunVisionSession): an answer
	// with nothing to ask runs a describe, and the question is ignored for
	// transcribe and describe entirely. Deriving the effective mode and
	// question before anything is computed from them keeps the cache key,
	// metadata and footer honest about the work actually performed — a
	// plain describe call shares the entry an answer-with-blank-question
	// wrote instead of storing the same picture twice.
	if mode == analyzeImageModeAnswer && strings.TrimSpace(input.Question) == "" {
		mode = analyzeImageModeDescribe
	}
	question := input.Question
	if mode != analyzeImageModeAnswer {
		question = ""
	}

	// The effort this call names wins over the one the run inherited, so one
	// analysis can think harder (or lighter) without moving the whole session.
	// The schema offers exactly three depths, and validImageEffort is the
	// tool's own check for them; anything else is a mistake to report, not a
	// value to send.
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
		return Result{}, fmt.Errorf("read image %s: %w", path, err)
	}
	if info.Size() > analyzeImageMaxBytes {
		return Result{}, fmt.Errorf("image %s is %.1f MB — analyze_image handles at most %d MB",
			path, float64(info.Size())/(1<<20), analyzeImageMaxBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read image %s: %w", path, err)
	}

	// The key hashes the file's CONTENT, not its path: a re-rendered or
	// re-saved copy of the same picture hits, and a genuinely new image does
	// not. The effective mode, and the question in answer mode, ride the key
	// because they change what is asked, and the resolved vision model and
	// provider because answers differ by model. So does the effort: a deep
	// diagnosis and a quick caption of one picture are different answers.
	model, providerID := tctx.Model, tctx.Provider
	if t.ResolveModel != nil {
		model, providerID = t.ResolveModel(model, providerID)
	}
	fileHash := fmt.Sprintf("%x", sha256.Sum256(data))
	key := webcache.Key(webcache.KindImage, fileHash, mode, question, model, providerID, effort)
	caching := t.Cache != nil && t.Cache.Enabled()

	if caching && !input.Fresh {
		if body, ok := t.Cache.Get(webcache.KindImage, key); ok && len(strings.TrimSpace(string(body))) > 0 {
			return Result{
				Title:    "Analyze image: " + filepath.Base(path),
				Output:   string(body) + analyzeImageCachedFooter(filepath.Base(path), mode),
				Metadata: analyzeImageMetadata(path, mode, true),
			}, nil
		}
	}

	// Payload on the wire. A JPEG or PNG small enough for a vision model
	// unchanged is passed through byte-for-byte — re-encoding costs OCR
	// fidelity. Anything else is decoded and re-encoded the same way
	// view_image prepares its attachments, which downscales past the
	// vision-model pixel limit and normalises the format.
	detected := http.DetectContentType(data)
	mediaType, payload := detected, data
	if detected != "image/jpeg" && detected != "image/png" || len(data) > analyzeImagePassthroughBytes {
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return Result{}, fmt.Errorf("unsupported or unreadable image %s (detected %s; analyze_image handles PNG, JPEG, GIF, BMP and WebP)", path, detected)
		}
		payload, mediaType, err = encodeImageForModel(img)
		if err != nil {
			return Result{}, fmt.Errorf("encode image %s: %w", path, err)
		}
	}

	// Bound the analysis with its own timeout so a slow vision model cannot
	// inherit an unbounded context from the parent loop.
	analyzeCtx, cancel := context.WithTimeout(ctx, analyzeImageTimeout)
	defer cancel()

	text, err := t.Run(analyzeCtx, payload, mediaType, mode, question, model, providerID, effort)
	if err != nil {
		return Result{Output: fmt.Sprintf("Image analysis error: %s", err)}, nil
	}
	text = strings.TrimSpace(text)

	// An error is never cached, and neither is an empty answer. A cache
	// failure is logged and swallowed: the tool's job is the text, not the
	// cache.
	if caching && text != "" {
		if err := t.Cache.Put(webcache.KindImage, key, []byte(text)); err != nil {
			slog.Warn("analyze_image: cache result", "err", err)
		}
	}

	result := Result{
		Title:    "Analyze image: " + filepath.Base(path),
		Output:   text,
		Metadata: analyzeImageMetadata(path, mode, false),
	}
	// No empty-text special case: the pipeline guarantees a non-empty
	// answer — oneShotVisionLLM falls back to the reasoning stream and
	// RunVisionSession errors out when the output is still empty.
	return result, nil
}

// Compile-time guard: AnalyzeImageTool satisfies the ToolDef interface.
var _ ToolDef = AnalyzeImageTool{}
