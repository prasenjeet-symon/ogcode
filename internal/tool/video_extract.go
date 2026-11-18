package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// Screen-recording frame extraction: ffmpeg decodes the video at a low sample
// rate, the comparison below decides which samples carry a change worth a
// model's attention, and the survivors land in frames/ next to a manifest.json
// that records the timestamps, change scores and any transcript lines.
//
// The audience is screen recordings, screencasts and slide decks — content that
// changes in small, localized bursts while a cursor drifts across it. Whole-
// frame scene detection is the wrong signal there (a dropdown opening is one
// percent of the frame; a moving cursor is noise), so changed-AREA ratio against
// the last kept frame, plus a dHash guard and a blur floor, is what selects.

// VideoExtractOptions configures one extraction run. Zero values take the
// defaults noted on each field.
type VideoExtractOptions struct {
	// Path is the absolute path of the video to read.
	Path string
	// OutDir receives frames/ and manifest.json. It is created if missing.
	OutDir string
	// SampleFPS is how many frames per second of video are decoded for
	// comparison (default 1). This is the rate the DIFF runs at, not the rate
	// of the output: screen recordings change far slower than they play.
	SampleFPS float64
	// MaxFrames caps how many frames are kept for the model (default 40).
	// Past roughly this many images in one analysis, attention to any single
	// frame drops regardless of the model's context window.
	MaxFrames int
	// ChangeThreshold is the fraction of the comparison grid that must differ
	// from the last kept frame before a sample is kept (default 0.01 — one
	// percent). A moving cursor sits below it; a menu opening sits above it.
	ChangeThreshold float64
	// BlurThreshold drops a sample whose 3x3 Laplacian variance falls below it
	// — the black frame of a transition, or a blank screen (default 8).
	BlurThreshold float64
	// WorkWidth is the width every decoded frame is scaled to for comparison
	// (default 480). Small enough that a cursor is a few pixels, large enough
	// that a line of changed text still registers.
	WorkWidth int
	// SegmentStart and SegmentEnd name a focus window, in seconds. The
	// recording is still sampled across its whole length at SampleFPS; inside
	// this window a second, denser pass samples it again. Both zero means no
	// window.
	SegmentStart float64
	SegmentEnd   float64
	// FocusFrames caps the detail frames kept inside the segment (default 20).
	// It is a budget on TOP of MaxFrames, which still caps the overview.
	FocusFrames int
}

// VideoFrameInfo is one kept frame, as both the manifest and the agent see it.
type VideoFrameInfo struct {
	// Index is 1-based among kept frames.
	Index int `json:"index"`
	// Seconds is the frame's timestamp in the video.
	Seconds float64 `json:"seconds"`
	// Stamp is that timestamp as "00:02:03".
	Stamp string `json:"stamp"`
	// Label is the timestamp as the model sees it: "[00:02:03]".
	Label string `json:"label"`
	// Path is the absolute path of the JPEG on disk.
	Path string `json:"path"`
	// Change is the changed-area ratio against the previous kept frame
	// (0 for the first frame).
	Change float64 `json:"change"`
	// Dropped is how many sampled frames were skipped between the previous
	// kept frame and this one.
	Dropped int `json:"dropped"`
	// Focus marks a frame from the segment's dense pass. The zero value marks
	// an overview frame.
	Focus bool `json:"focus,omitempty"`
}

// VideoTranscriptLine is one aligned line of a sidecar or embedded subtitle.
type VideoTranscriptLine struct {
	StartSeconds float64 `json:"startSeconds"`
	Stamp        string  `json:"stamp"`
	Text         string  `json:"text"`
}

// VideoManifest is what one extraction produced. It is written to
// OutDir/manifest.json and read back by a later call for the same unchanged
// file, so the frames are decoded once.
type VideoManifest struct {
	Source           string  `json:"source"`
	DurationSec      float64 `json:"durationSec"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	FPS              float64 `json:"fps"`
	SampleFPS        float64 `json:"sampleFPS"`
	Sampled          int     `json:"sampled"`
	Kept             int     `json:"kept"`
	Dropped          int     `json:"dropped"`
	BlurDropped      int     `json:"blurDropped"`
	DuplicateDropped int     `json:"duplicateDropped"`
	Omitted          int     `json:"omitted"`
	ChangeThreshold  float64 `json:"changeThreshold"`
	// The focus window, when the extraction had one: where it was, how
	// densely the dense pass sampled it and how many frames it kept.
	SegmentStartSec float64               `json:"segmentStartSec,omitempty"`
	SegmentEndSec   float64               `json:"segmentEndSec,omitempty"`
	FocusFPS        float64               `json:"focusFPS,omitempty"`
	FocusFrames     int                   `json:"focusFrames,omitempty"`
	FocusKept       int                   `json:"focusKept,omitempty"`
	Frames          []VideoFrameInfo      `json:"frames"`
	Transcript      []VideoTranscriptLine `json:"transcript,omitempty"`
	FramesDir       string                `json:"framesDir"`
	ManifestPath    string                `json:"manifestPath"`
}

// Selection constants. The compare grid is always this wide whatever the
// recording's resolution, so the thresholds below mean the same thing on a
// 720p capture and a 4K one.
const (
	videoCompareWidth    = 160
	videoPixelDelta      = 12
	videoDuplicateMax    = 3 // Hamming distance below which a sample is a copy
	videoDefaultFPS      = 1.0
	videoDefaultMax      = 40
	videoDefaultChange   = 0.01
	videoDefaultBlur     = 8.0
	videoDefaultWorkWdth = 480
	videoDefaultFocus    = 20   // detail frames kept inside a segment
	videoFocusMaxFPS     = 10.0 // ceiling on the segment's own sample rate

	// videoDuplicateMinChange is how much of the frame must have changed
	// before the dHash guard may fire at all. A menu opening, a dialog or a
	// line of new text changes a few percent of the frame and leaves the 9x8
	// structure hash almost untouched (measured: 4% changed, Hamming 1-2), so
	// the guard can only catch a copy when the whole frame churned and still
	// hashed the same — a re-render or a brightness shift — never a localized
	// change, which is the case this tool exists for.
	videoDuplicateMinChange = 0.5
)

// ExtractVideoFrames samples the video, keeps the frames whose content changed,
// writes them to opts.OutDir/frames and returns the manifest. A missing ffmpeg
// or ffprobe is reported with the install hint rather than an exec error, and a
// video with no decodable frames is an error rather than an empty manifest.
func ExtractVideoFrames(ctx context.Context, opts VideoExtractOptions) (*VideoManifest, error) {
	if opts.Path == "" {
		return nil, errors.New("analyze_video: path is required")
	}
	if opts.OutDir == "" {
		return nil, errors.New("analyze_video: outDir is required")
	}
	source, err := filepath.Abs(opts.Path)
	if err != nil {
		source = opts.Path
	}
	if _, err := os.Stat(source); err != nil {
		return nil, fmt.Errorf("read video %s: %w", source, err)
	}
	ffmpegPath, err := videoBinary("ffmpeg")
	if err != nil {
		return nil, err
	}
	ffprobePath, err := videoBinary("ffprobe")
	if err != nil {
		return nil, err
	}
	pb, err := videoRunProbe(ctx, ffprobePath, source)
	if err != nil {
		return nil, err
	}

	if opts.SampleFPS <= 0 {
		opts.SampleFPS = videoDefaultFPS
	}
	if opts.MaxFrames <= 0 {
		opts.MaxFrames = videoDefaultMax
	}
	if opts.ChangeThreshold <= 0 {
		opts.ChangeThreshold = videoDefaultChange
	}
	if opts.BlurThreshold <= 0 {
		opts.BlurThreshold = videoDefaultBlur
	}
	if opts.WorkWidth <= 0 {
		opts.WorkWidth = videoDefaultWorkWdth
	}
	if opts.FocusFrames <= 0 {
		opts.FocusFrames = videoDefaultFocus
	}
	// Never upscale: a 320px capture sampled at 480 would just blur itself.
	if opts.WorkWidth > pb.Width {
		opts.WorkWidth = pb.Width
	}
	if opts.WorkWidth < 1 {
		opts.WorkWidth = 1
	}

	framesDir := filepath.Join(opts.OutDir, "frames")

	// The focus window is clamped to the recording, and a window that starts
	// past its end is a mis-aimed call rather than an empty pass.
	segStart, segEnd := opts.SegmentStart, opts.SegmentEnd
	if segStart < 0 {
		segStart = 0
	}
	hasSegment := opts.SegmentEnd > opts.SegmentStart
	if hasSegment && segStart >= pb.Duration {
		return nil, fmt.Errorf("segment starts at %s, past the end of the %s recording",
			videoStamp(segStart), videoStamp(pb.Duration))
	}
	if segEnd > pb.Duration {
		segEnd = pb.Duration
	}
	if segEnd <= segStart {
		hasSegment = false
		segStart, segEnd = 0, 0
	}

	// The overview: the whole recording, one sample per 1/SampleFPS, with the
	// window left to the dense pass so no moment is described twice.
	overview, err := videoSamplePass(ctx, ffmpegPath, source, videoPassSpec{
		rawDir:    filepath.Join(opts.OutDir, "raw"),
		sampleFPS: opts.SampleFPS,
		skipFrom:  segStart,
		skipTo:    segEnd,
		maxFrames: opts.MaxFrames,
	}, opts)
	if err != nil {
		return nil, err
	}
	passes := []*videoPass{overview}

	focusFPS := 0.0
	if hasSegment {
		window := segEnd - segStart
		// Never less densely than the rest of the recording, never faster
		// than the cap: the frame budget, not the clock, is what keeps the
		// analysis affordable.
		focusFPS = math.Min(videoFocusMaxFPS, math.Max(opts.SampleFPS, float64(opts.FocusFrames)/window))
		focus, err := videoSamplePass(ctx, ffmpegPath, source, videoPassSpec{
			rawDir:    filepath.Join(opts.OutDir, "raw-focus"),
			sampleFPS: focusFPS,
			spanStart: segStart,
			spanDur:   window,
			maxFrames: opts.FocusFrames,
			focus:     true,
		}, opts)
		if err != nil {
			return nil, err
		}
		passes = append(passes, focus)
	}

	// One chronological timeline: the overview before the window, the dense
	// pass inside it, the overview after it.
	var keeps []videoKeep
	sampled, selected, blurDropped, duplicateDropped, omitted := 0, 0, 0, 0, 0
	for _, pass := range passes {
		keeps = append(keeps, pass.keeps...)
		sampled += pass.sampled
		selected += pass.selected
		blurDropped += pass.blurDropped
		duplicateDropped += pass.duplicateDropped
		omitted += pass.omitted
	}
	if sampled == 0 {
		return nil, fmt.Errorf("extracting frames from %s produced no images — the file has a video stream ffprobe can see but no frames ffmpeg can decode", source)
	}
	if hasSegment && passes[len(passes)-1].sampled == 0 {
		return nil, fmt.Errorf("segment %s–%s is too short to sample: no frame decoded between those timestamps",
			videoStamp(segStart), videoStamp(segEnd))
	}
	sort.SliceStable(keeps, func(i, j int) bool { return keeps[i].seconds < keeps[j].seconds })
	if len(keeps) == 0 {
		return nil, fmt.Errorf("no frame of %s survived selection — every sample was unchanged, blurred or blank", source)
	}

	// Write the surviving frames, then drop the raw samples: only the kept
	// JPEGs are meant to outlive the call.
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare frames dir %s: %w", framesDir, err)
	}
	frames := make([]VideoFrameInfo, 0, len(keeps))
	focusKept := 0
	for n, k := range keeps {
		data, err := os.ReadFile(k.rawPath)
		if err != nil {
			return nil, fmt.Errorf("read kept sample %s: %w", k.rawPath, err)
		}
		name := fmt.Sprintf("%06d_%s.jpg", n+1, videoFileStamp(k.seconds))
		dst := filepath.Join(framesDir, name)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, fmt.Errorf("write frame %s: %w", dst, err)
		}
		stamp := videoStamp(k.seconds)
		frames = append(frames, VideoFrameInfo{
			Index:   n + 1,
			Seconds: k.seconds,
			Stamp:   stamp,
			Label:   "[" + stamp + "]",
			Path:    dst,
			Change:  k.change,
			Dropped: k.dropped,
			Focus:   k.focus,
		})
		if k.focus {
			focusKept++
		}
	}
	for _, dir := range []string{filepath.Join(opts.OutDir, "raw"), filepath.Join(opts.OutDir, "raw-focus")} {
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("remove raw samples %s: %w", dir, err)
		}
	}

	transcript := videoReadSubtitles(source)

	focusFrames := 0
	if hasSegment {
		focusFrames = opts.FocusFrames
	}
	manifest := &VideoManifest{
		Source:           source,
		DurationSec:      pb.Duration,
		Width:            pb.Width,
		Height:           pb.Height,
		FPS:              pb.FPS,
		SampleFPS:        opts.SampleFPS,
		Sampled:          sampled,
		Kept:             len(frames),
		Dropped:          sampled - selected,
		BlurDropped:      blurDropped,
		DuplicateDropped: duplicateDropped,
		Omitted:          omitted,
		ChangeThreshold:  opts.ChangeThreshold,
		SegmentStartSec:  segStart,
		SegmentEndSec:    segEnd,
		FocusFPS:         focusFPS,
		FocusFrames:      focusFrames,
		FocusKept:        focusKept,
		Frames:           frames,
		Transcript:       transcript,
		FramesDir:        framesDir,
		ManifestPath:     filepath.Join(opts.OutDir, "manifest.json"),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(manifest.ManifestPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	return manifest, nil
}

// videoKeep is one selected sample, on the recording's own timeline.
type videoKeep struct {
	seconds float64
	change  float64
	dropped int
	// rawPath is the JPEG ffmpeg wrote for this sample, dropped once the
	// kept frames have been copied out.
	rawPath string
	// focus marks a keep from the segment's dense pass.
	focus bool
}

// videoPass is one sampling round: ffmpeg decodes one span of the recording at
// one rate, selection keeps the samples whose content changed, and the keeps
// come back with their timestamps.
type videoPass struct {
	sampled          int
	selected         int
	blurDropped      int
	duplicateDropped int
	omitted          int
	keeps            []videoKeep
}

// videoPassSpec is one round's knobs. spanStart/spanDur narrow the decode to a
// slice of the file; skipFrom/skipTo, when skipTo > skipFrom, drop samples
// inside a window from the result BEFORE selection.
type videoPassSpec struct {
	rawDir    string
	sampleFPS float64
	spanStart float64
	spanDur   float64
	skipFrom  float64
	skipTo    float64
	maxFrames int
	focus     bool
}

// videoSamplePass decodes one span of the recording at sampleFPS and returns
// the samples whose content changed. skipFrom/skipTo, when skipTo > skipFrom,
// remove samples inside that window from the result before selection: the
// segment's dense pass owns those moments, and the first sample after the
// window must still be measured against the last one kept before it.
func videoSamplePass(ctx context.Context, ffmpegPath, source string, spec videoPassSpec, opts VideoExtractOptions) (*videoPass, error) {
	pass := &videoPass{}
	if err := os.RemoveAll(spec.rawDir); err != nil {
		return nil, fmt.Errorf("clear extract dir %s: %w", spec.rawDir, err)
	}
	if err := os.MkdirAll(spec.rawDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare extract dir %s: %w", spec.rawDir, err)
	}

	// One JPEG per 1/SampleFPS of video, at the comparison width. The window
	// knobs are INPUT options, so ffmpeg seeks to the span instead of decoding
	// the recording up to it; -2 on the height keeps it even for the chroma
	// subsampling.
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if spec.spanStart > 0 {
		args = append(args, "-ss", strconv.FormatFloat(spec.spanStart, 'f', 3, 64))
	}
	if spec.spanDur > 0 {
		args = append(args, "-t", strconv.FormatFloat(spec.spanDur, 'f', 3, 64))
	}
	args = append(args,
		"-i", source,
		"-vf", fmt.Sprintf("fps=%s,scale=%d:-2",
			strconv.FormatFloat(spec.sampleFPS, 'f', -1, 64), opts.WorkWidth),
		"-q:v", "4",
		filepath.Join(spec.rawDir, "%06d.jpg"),
	)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	setProcessGroup(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("extracting frames from %s: %w", source, ctx.Err())
		}
		return nil, fmt.Errorf("ffmpeg sampling %s failed: %v: %s",
			source, err, strings.TrimSpace(stderr.String()))
	}
	sampleFiles, err := videoJPEGS(spec.rawDir)
	if err != nil {
		return nil, err
	}

	// Decode every sample once at the comparison grid: the change score, the
	// dHash and the blur floor all read the same 160-wide grayscale.
	type sample struct {
		seconds float64
		rawPath string
		grid    *image.Gray
		hash    uint64
		blur    float64
	}
	samples := make([]sample, 0, len(sampleFiles))
	seen := 0
	for i, name := range sampleFiles {
		seconds := spec.spanStart + float64(i)/spec.sampleFPS
		if spec.skipTo > spec.skipFrom && seconds >= spec.skipFrom && seconds < spec.skipTo {
			continue
		}
		seen++
		rawPath := filepath.Join(spec.rawDir, name)
		data, err := os.ReadFile(rawPath)
		if err != nil {
			return nil, fmt.Errorf("read sample %s: %w", name, err)
		}
		grid, err := videoGrayGrid(data)
		if err != nil {
			// A sample ffmpeg wrote but the JPEG decoder rejects is a bad
			// frame, not a bad video: skip it rather than fail the run.
			continue
		}
		samples = append(samples, sample{
			seconds: seconds,
			rawPath: rawPath,
			grid:    grid,
			hash:    videoDHash(grid),
			blur:    videoLaplacianVariance(grid),
		})
	}
	// Nothing decoded is not an error here: a window that falls outside the
	// decoded frames is the caller's to judge, and a file with no decodable
	// frames is diagnosed once both passes have run.
	if seen == 0 {
		return pass, nil
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("extracting frames from %s produced images that could not be decoded as JPEG", source)
	}

	// Selection: each sample is compared against the LAST KEPT frame, not the
	// previous sample — that is what lets a slow, steady change eventually
	// register and a one-off flicker not. The first sample is always kept.
	type kept struct {
		sampleIdx int
		change    float64
		dropped   int
	}
	var keeps []kept
	lastKeptGrid, lastKeptHash := samples[0].grid, samples[0].hash
	blurDropped, duplicateDropped, skipped := 0, 0, 0
	appendKept := func(i, drop int, chg float64) {
		keeps = append(keeps, kept{sampleIdx: i, change: chg, dropped: drop})
		lastKeptGrid, lastKeptHash = samples[i].grid, samples[i].hash
		skipped = 0
	}
	for i := range samples {
		if i == 0 {
			appendKept(0, 0, 0)
			continue
		}
		score := videoChangeScore(samples[i].grid, lastKeptGrid)
		switch {
		case score < opts.ChangeThreshold:
			skipped++
		case samples[i].blur < opts.BlurThreshold:
			blurDropped++
			skipped++
		case score > videoDuplicateMinChange && videoHammingDistance(samples[i].hash, lastKeptHash) < videoDuplicateMax:
			duplicateDropped++
			skipped++
		default:
			appendKept(i, skipped, score)
		}
	}

	// The pass's frame cap. Evenly spaced — never bunched at the start — and
	// always including the first and last frame.
	pass.sampled = len(samples)
	pass.selected = len(keeps)
	pass.blurDropped = blurDropped
	pass.duplicateDropped = duplicateDropped
	chosen := videoEvenSubset(len(keeps), spec.maxFrames)
	pass.omitted = len(keeps) - len(chosen)
	for _, i := range chosen {
		k := keeps[i]
		pass.keeps = append(pass.keeps, videoKeep{
			seconds: samples[k.sampleIdx].seconds,
			change:  k.change,
			dropped: k.dropped,
			rawPath: samples[k.sampleIdx].rawPath,
			focus:   spec.focus,
		})
	}
	return pass, nil
}

// videoProbe is one video's width, height, frame rate and duration as ffprobe
// reports them. A stream ffprobe cannot find, or a duration it cannot read, is
// a clear error: there is nothing to sample.
type videoProbe struct {
	Width    int
	Height   int
	FPS      float64
	Duration float64
}

func videoRunProbe(ctx context.Context, ffprobePath, path string) (*videoProbe, error) {
	ctxDone := func() (*videoProbe, error) {
		return nil, fmt.Errorf("probing %s: %w", path, ctx.Err())
	}
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate:format=duration",
		"-of", "json", path)
	setProcessGroup(cmd)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctxDone()
		}
		return nil, fmt.Errorf("ffprobe %s failed: %v: %s", path, err, strings.TrimSpace(errOut.String()))
	}
	var report struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		return nil, fmt.Errorf("parse ffprobe output for %s: %w", path, err)
	}
	if len(report.Streams) == 0 || report.Streams[0].Width == 0 || report.Streams[0].Height == 0 {
		return nil, fmt.Errorf("video %s has no video stream", path)
	}
	duration, _ := strconv.ParseFloat(strings.TrimSpace(report.Format.Duration), 64)
	if duration <= 0 {
		return nil, fmt.Errorf("video %s reports no duration — there is nothing to sample", path)
	}
	pb := &videoProbe{Width: report.Streams[0].Width, Height: report.Streams[0].Height, Duration: duration}
	if num, den, ok := strings.Cut(report.Streams[0].FrameRate, "/"); ok {
		n, d := parseFloat64(num), parseFloat64(den)
		if d > 0 && n > 0 {
			pb.FPS = n / d
		}
	}
	return pb, nil
}

func parseFloat64(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func videoBinary(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("analyze_video needs %s on PATH — install it (on macOS: brew install ffmpeg) and try again", name)
	}
	return path, nil
}

// videoJPEGS lists the sampled raw JPEGs, ffmpeg's %06d numbering being
// already lexicographic order.
func videoJPEGS(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list raw samples in %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// videoGrayGrid decodes one raw JPEG into the 160-wide grayscale comparison
// grid every selection metric below reads.
func videoGrayGrid(data []byte) (*image.Gray, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode sample: %w", err)
	}
	b := src.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 {
		return nil, errors.New("sample is empty")
	}
	gridH := max(1, b.Dy()*videoCompareWidth/b.Dx())
	dst := image.NewGray(image.Rect(0, 0, videoCompareWidth, gridH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst, nil
}

// videoDHash is the horizontal 9x8 difference hash: 64 bits, computed on the
// comparison grid, that catches a sample which scored high on pixel change but
// is structurally the same image (sensor noise, re-rendered text).
func videoDHash(g *image.Gray) uint64 {
	small := image.NewGray(image.Rect(0, 0, 9, 8))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), g, g.Bounds(), draw.Src, nil)
	var hash uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if small.GrayAt(x, y).Y < small.GrayAt(x+1, y).Y {
				hash |= 1 << (y*8 + x)
			}
		}
	}
	return hash
}

func videoHammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// videoChangeScore is the changed-area ratio: the fraction of the comparison
// grid whose pixels differ by more than videoPixelDelta. Against the last kept
// frame, a drifting cursor stays under a percent of the grid; an opened menu
// or a slide transition clears it easily.
func videoChangeScore(a, b *image.Gray) float64 {
	aBounds, bBounds := a.Bounds(), b.Bounds()
	w, h := aBounds.Dx(), aBounds.Dy()
	if bBounds.Dx() < w {
		w = bBounds.Dx()
	}
	if bBounds.Dy() < h {
		h = bBounds.Dy()
	}
	if w < 1 || h < 1 {
		return 1
	}
	changed, total := 0, w*h
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			diff := int(a.GrayAt(aBounds.Min.X+x, aBounds.Min.Y+y).Y) -
				int(b.GrayAt(bBounds.Min.X+x, bBounds.Min.Y+y).Y)
			if diff < -videoPixelDelta || diff > videoPixelDelta {
				changed++
			}
		}
	}
	return float64(changed) / float64(total)
}

// videoLaplacianVariance is the blur floor: a 3x3 Laplacian over the grid, its
// variance collapsing to almost nothing on the black frame of a transition or
// an out-of-focus capture.
func videoLaplacianVariance(g *image.Gray) float64 {
	b := g.Bounds()
	if b.Dx() < 3 || b.Dy() < 3 {
		return 0
	}
	var sum, sumSq float64
	count := 0
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		for x := b.Min.X + 1; x < b.Max.X-1; x++ {
			center := int(g.GrayAt(x, y).Y)
			lap := 4*center - int(g.GrayAt(x-1, y).Y) - int(g.GrayAt(x+1, y).Y) -
				int(g.GrayAt(x, y-1).Y) - int(g.GrayAt(x, y+1).Y)
			sum += float64(lap)
			sumSq += float64(lap) * float64(lap)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	mean := sum / float64(count)
	return sumSq/float64(count) - mean*mean
}

// videoEvenSubset picks at most maxFrames indices evenly across n survivors,
// always the first and the last. It is the frame cap: a capped timeline is
// sampled, not truncated to its opening seconds.
func videoEvenSubset(n, maxFrames int) []int {
	if n <= 0 {
		return nil
	}
	if maxFrames <= 0 || n <= maxFrames {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	step := float64(n-1) / float64(maxFrames-1)
	idx := make([]int, 0, maxFrames)
	for k := 0; k < maxFrames; k++ {
		i := int(math.Round(float64(k) * step))
		if i > n-1 {
			i = n - 1
		}
		if len(idx) == 0 || idx[len(idx)-1] != i {
			idx = append(idx, i)
		}
	}
	if idx[0] != 0 {
		idx[0] = 0
	}
	if idx[len(idx)-1] != n-1 {
		idx[len(idx)-1] = n - 1
	}
	return idx
}

// videoReadSubtitles aligns whatever transcript the video carries: a sidecar
// .srt or .vtt next to it (srt preferred), else an embedded subtitle track
// piped through ffmpeg. A recording with no subtitles at all is the normal
// case, so every failure here is silent and leaves an empty transcript.
func videoReadSubtitles(source string) []VideoTranscriptLine {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	for _, ext := range []string{".srt", ".vtt"} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(source), base+ext))
		if err != nil {
			continue
		}
		if lines := videoParseSubtitles(data); len(lines) > 0 {
			return lines
		}
	}
	cmd := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", source, "-map", "0:s:0", "-f", "srt", "pipe:1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return nil
	}
	return videoParseSubtitles(out.Bytes())
}

// videoParseSubtitles reads srt or vtt cue text into transcript lines. Both
// carry the same "HH:MM:SS --> HH:MM:SS" timing line; they differ only in the
// decimal comma and in vtt's optional cue settings, both handled below.
func videoParseSubtitles(data []byte) []VideoTranscriptLine {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var lines []VideoTranscriptLine
	for _, block := range strings.Split(text, "\n\n") {
		var body []string
		var start float64
		hasTiming := false
		for _, row := range strings.Split(block, "\n") {
			switch {
			case !hasTiming && strings.Contains(row, "-->"):
				side, _, ok := strings.Cut(row, "-->")
				if !ok {
					body = nil
					break
				}
				// The left side is the start time (srt has no cue id here;
				// a vtt id line, if any, sat above this row and is ignored).
				startField := strings.Fields(strings.TrimSpace(side))
				last := ""
				if len(startField) > 0 {
					last = startField[len(startField)-1]
				}
				secs, ok := videoSubtitleSeconds(last)
				if !ok {
					body = nil
				}
				start, hasTiming = secs, ok
			case hasTiming:
				body = append(body, row)
			default:
				// An srt sequence number or a vtt cue identifier: skip it.
			}
		}
		if !hasTiming {
			continue
		}
		text := strings.TrimSpace(videoStripCueMarkup(strings.Join(body, " ")))
		if text == "" {
			continue
		}
		lines = append(lines, VideoTranscriptLine{
			StartSeconds: start,
			Stamp:        videoStamp(start),
			Text:         text,
		})
	}
	return lines
}

// videoSubtitleSeconds parses "00:00:01,000" (srt), "00:00:01.000" or the
// vtt form without hours ("04:05.000").
func videoSubtitleSeconds(s string) (float64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	hours, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	minutes, secondsPart, ok := strings.Cut(rest, ":")
	if !ok {
		// "MM:SS.mmm" — the value seen so far was the minutes.
		minutes, secondsPart = hours, rest
		hours = "0"
	}
	h, m := parseFloat64(hours), parseFloat64(minutes)
	sec, err := strconv.ParseFloat(strings.TrimSpace(secondsPart), 64)
	if err != nil || h < 0 || m < 0 || sec < 0 {
		return 0, false
	}
	return h*3600 + m*60 + sec, true
}

// videoStripCueMarkup removes the emphasis tags (<i>, <b>, <c.pos>) subtitles
// wrap their text in, and the few entities that survive a plain-text read.
func videoStripCueMarkup(s string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '<')
		if open < 0 {
			break
		}
		end := strings.IndexByte(s[open:], '>')
		if end < 0 {
			break
		}
		b.WriteString(s[:open])
		s = s[open+end+1:]
	}
	b.WriteString(s)
	out := b.String()
	for _, ent := range []struct{ from, to string }{
		{"&nbsp;", " "}, {"&amp;", "&"}, {"&lt;", "<"},
		{"&gt;", ">"}, {"&quot;", `"`}, {"&#39;", "'"},
	} {
		out = strings.ReplaceAll(out, ent.from, ent.to)
	}
	return out
}

// videoStamp formats a timestamp as "00:02:03" — hours always present, so the
// label a model reads is unambiguous whatever the recording's length.
func videoStamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// videoFileStamp is the same timestamp as a filename-safe "00m02s03".
func videoFileStamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%02dm%02ds%02d", total/3600, (total%3600)/60, total%60)
}
