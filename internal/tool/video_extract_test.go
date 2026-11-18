package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newVideoTestGray builds a w×h grayscale grid filled with fill, with the
// rectangle at (x0,y0) set to rect — the smallest vocabulary the selection
// math tests need.
func newVideoTestGray(w, h, fill, x0, y0, bw, bh, rect int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g.SetGray(x, y, color.Gray{Y: uint8(fill)})
		}
	}
	for y := y0; y < y0+bh && y < h; y++ {
		for x := x0; x < x0+bw && x < w; x++ {
			g.SetGray(x, y, color.Gray{Y: uint8(rect)})
		}
	}
	return g
}

func TestVideoChangeScore(t *testing.T) {
	tests := []struct {
		name             string
		a, b             *image.Gray
		wantMin, wantMax float64
	}{
		{"identical flat frames",
			newVideoTestGray(160, 120, 100, 0, 0, 0, 0, 0),
			newVideoTestGray(160, 120, 100, 0, 0, 0, 0, 0),
			0, 0},
		{"small moved block stays under the default threshold",
			newVideoTestGray(160, 120, 100, 20, 20, 5, 5, 200),
			newVideoTestGray(160, 120, 100, 60, 60, 5, 5, 200),
			0, 0.01},
		{"a large change clears the default threshold",
			newVideoTestGray(160, 120, 100, 0, 0, 0, 0, 0),
			newVideoTestGray(160, 120, 100, 40, 20, 60, 60, 200),
			0.01, 1},
	}
	for _, tt := range tests {
		score := videoChangeScore(tt.a, tt.b)
		if score < tt.wantMin || score > tt.wantMax {
			t.Fatalf("%s: change score %v outside [%v, %v]", tt.name, score, tt.wantMin, tt.wantMax)
		}
	}
}

func TestVideoDHash(t *testing.T) {
	// Identical grids hash identically; a high-frequency pattern shifted a
	// couple of pixels lands far away, far past the duplicate window.
	same := image.NewGray(image.Rect(0, 0, 160, 120))
	noise := image.NewGray(image.Rect(0, 0, 160, 120))
	shifted := image.NewGray(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			v := uint8((x*x + y) * 31 % 251)
			noise.SetGray(x, y, color.Gray{Y: v})
			if x >= 2 {
				// Content moved right by 2: column x holds what was at x-2.
				shifted.SetGray(x, y, color.Gray{Y: uint8(((x-2)*(x-2) + y) * 31 % 251)})
			}
		}
	}
	if d := videoHammingDistance(videoDHash(same), videoDHash(same)); d != 0 {
		t.Fatalf("identical frames: hamming distance %d, want 0", d)
	}
	if d := videoHammingDistance(videoDHash(noise), videoDHash(noise)); d != 0 {
		t.Fatalf("same grid twice: hamming distance %d, want 0", d)
	}
	if d := videoHammingDistance(videoDHash(noise), videoDHash(shifted)); d < 3 {
		t.Fatalf("shifted noise: hamming distance %d, want >= 3 (would be treated as a duplicate)", d)
	}
}

func TestVideoLaplacianVariance(t *testing.T) {
	flat := image.NewGray(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			flat.SetGray(x, y, color.Gray{Y: 128})
		}
	}
	if v := videoLaplacianVariance(flat); v > 1 {
		t.Fatalf("flat grid: laplacian variance %v, want <= 1 (a blurry frame must be droppable)", v)
	}
	textured := image.NewGray(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			if (x/2+y/2)%2 == 0 {
				textured.SetGray(x, y, color.Gray{Y: 0})
			} else {
				textured.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	if v := videoLaplacianVariance(textured); v <= 8 {
		t.Fatalf("checkerboard: laplacian variance %v, want > 8 (a sharp frame must survive)", v)
	}
}

func TestVideoSubtitleSeconds(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"00:00:01,000", 1, true},
		{"00:01:02,750", 62.75, true},
		{"01:02:03.5", 3723.5, true},
		{"02:03.500", 123.5, true},
		{"00:00:00,000", 0, true},
		{"nope", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := videoSubtitleSeconds(tt.in)
		if ok != tt.ok {
			t.Fatalf("videoSubtitleSeconds(%q): ok=%v, want %v", tt.in, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Fatalf("videoSubtitleSeconds(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestVideoParseSubtitles(t *testing.T) {
	srt := []byte("1\n00:00:01,000 --> 00:00:04,000\n<i>Hello</i> &amp; welcome\n\n" +
		"2\n00:00:05,000 --> 00:00:06,000\n\n" +
		"3\n00:00:07,000 --> 00:00:09,000\nsecond line\n" +
		"still in the same cue\n")
	lines := videoParseSubtitles(srt)
	if len(lines) != 2 {
		t.Fatalf("srt: got %d transcript lines, want 2 (the empty cue and its number are skipped)", len(lines))
	}
	if lines[0].StartSeconds != 1 || lines[0].Stamp != "00:00:01" || lines[0].Text != "Hello & welcome" {
		t.Fatalf("srt first line = %+v, want start 1, stamp 00:00:01, tags and entities stripped", lines[0])
	}
	if lines[1].StartSeconds != 7 || lines[1].Text != "second line still in the same cue" {
		t.Fatalf("srt second line = %+v, want both cue rows joined", lines[1])
	}

	vtt := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:03.000 align:start line:50%\nFirst cue\n\n" +
		"note-cue\n00:01:00.000 --> 00:01:02.000\n<c.pos>Later</c.pos>\n")
	vlines := videoParseSubtitles(vtt)
	if len(vlines) != 2 {
		t.Fatalf("vtt: got %d transcript lines, want 2", len(vlines))
	}
	if vlines[0].Text != "First cue" || vlines[0].StartSeconds != 1 {
		t.Fatalf("vtt first line = %+v, cue settings must not leak into the text", vlines[0])
	}
	if vlines[1].StartSeconds != 60 || vlines[1].Text != "Later" {
		t.Fatalf("vtt second line = %+v, want the identifier line skipped and tags stripped", vlines[1])
	}

	if lines := videoParseSubtitles(nil); len(lines) != 0 {
		t.Fatalf("empty input: got %d lines, want 0", len(lines))
	}
}

func TestVideoStampFormats(t *testing.T) {
	tests := []struct {
		seconds   float64
		stamp     string
		fileStamp string
	}{
		{0, "00:00:00", "00m00s00"},
		{2.4, "00:00:02", "00m00s02"},
		{62.9, "00:01:02", "00m01s02"},
		{3725, "01:02:05", "01m02s05"},
		{-1, "00:00:00", "00m00s00"},
	}
	for _, tt := range tests {
		if got := videoStamp(tt.seconds); got != tt.stamp {
			t.Fatalf("videoStamp(%v) = %q, want %q", tt.seconds, got, tt.stamp)
		}
		if got := videoFileStamp(tt.seconds); got != tt.fileStamp {
			t.Fatalf("videoFileStamp(%v) = %q, want %q", tt.seconds, got, tt.fileStamp)
		}
	}
}

func TestVideoEvenSubset(t *testing.T) {
	tests := []struct {
		name         string
		n, maxFrames int
		wantLen      int
	}{
		{"under the cap keeps everything", 5, 40, 5},
		{"cap exactly", 40, 40, 40},
		{"cap trims", 10, 4, 4},
		{"cap trims a long run", 100, 40, 40},
	}
	for _, tt := range tests {
		idx := videoEvenSubset(tt.n, tt.maxFrames)
		if len(idx) != tt.wantLen {
			t.Fatalf("%s: got %d indices, want %d", tt.name, len(idx), tt.wantLen)
		}
		if len(idx) == 0 {
			continue
		}
		if idx[0] != 0 || idx[len(idx)-1] != tt.n-1 {
			t.Fatalf("%s: endpoints %d..%d, want 0..%d", tt.name, idx[0], idx[len(idx)-1], tt.n-1)
		}
		for i := 1; i < len(idx); i++ {
			if idx[i] <= idx[i-1] {
				t.Fatalf("%s: indices not strictly increasing at %d: %v", tt.name, i, idx)
			}
		}
	}
	if idx := videoEvenSubset(0, 40); len(idx) != 0 {
		t.Fatalf("empty input: got %v, want nothing", idx)
	}
	if idx := videoEvenSubset(10, 0); len(idx) != 10 {
		t.Fatalf("no cap: got %d indices, want 10", len(idx))
	}
}

func TestExtractVideoFramesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := t.TempDir()
	videoPath := filepath.Join(out, "rec.mp4")
	gen := exec.Command("ffmpeg", "-y", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=10",
		"-pix_fmt", "yuv420p", videoPath)
	if genOut, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, genOut)
	}
	// A sidecar next to the recording must ride the manifest.
	srt := "1\n00:00:01,000 --> 00:00:02,000\nopening screen\n"
	if err := os.WriteFile(filepath.Join(out, "rec.srt"), []byte(srt), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	manifest, err := ExtractVideoFrames(context.Background(), VideoExtractOptions{
		Path:   videoPath,
		OutDir: out,
	})
	if err != nil {
		t.Fatalf("ExtractVideoFrames: %v", err)
	}
	if len(manifest.Frames) < 1 {
		t.Fatalf("got %d frames, want >= 1", len(manifest.Frames))
	}
	if manifest.Source != videoPath {
		t.Fatalf("manifest.Source = %q, want the path passed in %q (a later call compares them)", manifest.Source, videoPath)
	}
	if manifest.Sampled < manifest.Kept || manifest.Kept != len(manifest.Frames) {
		t.Fatalf("counters inconsistent: sampled %d, kept %d, frames %d",
			manifest.Sampled, manifest.Kept, len(manifest.Frames))
	}
	for _, frame := range manifest.Frames {
		data, err := os.ReadFile(frame.Path)
		if err != nil {
			t.Fatalf("frame file %s: %v", frame.Path, err)
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("frame %s is not a decodable JPEG: %v", frame.Path, err)
		}
		if frame.Index < 1 || frame.Stamp == "" || frame.Label != "["+frame.Stamp+"]" {
			t.Fatalf("frame %+v: index/stamp/label not populated", frame)
		}
		wantName := filepath.Join(out, "frames",
			fmt.Sprintf("%06d_%s.jpg", frame.Index, videoFileStamp(frame.Seconds)))
		if frame.Path != wantName {
			t.Fatalf("frame path = %q, want %q", frame.Path, wantName)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "raw")); !os.IsNotExist(err) {
		t.Fatalf("raw sample directory left behind: %v", err)
	}
	data, err := os.ReadFile(manifest.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var roundTrip VideoManifest
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("parse manifest back: %v", err)
	}
	if len(roundTrip.Frames) != len(manifest.Frames) {
		t.Fatalf("manifest on disk has %d frames, want %d", len(roundTrip.Frames), len(manifest.Frames))
	}
	if len(roundTrip.Transcript) != 1 || roundTrip.Transcript[0].Text != "opening screen" {
		t.Fatalf("transcript from sidecar = %+v, want one line reading 'opening screen'", roundTrip.Transcript)
	}
}

// TestExtractVideoFramesKeepsLocalizedChange pins the screen-recording case:
// a menu, dialog or line of text changing a few percent of an otherwise static
// frame leaves the global dHash almost untouched (measured: a 200x60 box on a
// 640x360 frame is 5.2% changed, Hamming 2), so a duplicate guard that fired on
// the hash alone would drop exactly the frames this tool exists for.
func TestExtractVideoFramesKeepsLocalizedChange(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := t.TempDir()
	videoPath := filepath.Join(out, "screencast.mp4")
	gen := exec.Command("ffmpeg", "-y", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=0x111111:s=640x360:d=8:rate=10",
		"-vf", "drawbox=x=40:y=30:w=260:h=12:color=0xdddddd@1:t=fill,"+
			"drawbox=x=40:y=60:w=380:h=12:color=0xdddddd@1:t=fill,"+
			"drawbox=x=300:y=200:w=200:h=60:color=0x2a5caa@1:t=fill:enable='between(t,4,5.9)'",
		"-pix_fmt", "yuv420p", videoPath)
	if genOut, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, genOut)
	}

	manifest, err := ExtractVideoFrames(context.Background(), VideoExtractOptions{Path: videoPath, OutDir: out})
	if err != nil {
		t.Fatalf("ExtractVideoFrames: %v", err)
	}
	opened := false
	for _, frame := range manifest.Frames {
		if frame.Seconds >= 3.5 && frame.Seconds < 6 {
			opened = true
		}
		if frame.Seconds > 0.5 && frame.Seconds < 3.5 {
			t.Errorf("a static second was kept as a frame: %s", frame.Label)
		}
	}
	if !opened {
		t.Fatalf("the menu opening at t=4 was not kept: kept %d, dropped %d (duplicate %d, blur %d)",
			manifest.Kept, manifest.Dropped, manifest.DuplicateDropped, manifest.BlurDropped)
	}
	if manifest.DuplicateDropped != 0 {
		t.Errorf("a localized change was counted as a duplicate: %d", manifest.DuplicateDropped)
	}
}

func TestParseVideoSegment(t *testing.T) {
	cases := []struct {
		in         string
		start, end float64
		ok         bool
	}{
		{"", 0, 0, false},
		{"04:10-04:40", 250, 280, true},
		{"00:04:10-00:04:40", 250, 280, true},
		{"250-280.5", 250, 280.5, true},
		{" 1:00 - 1:30 ", 60, 90, true},
	}
	for _, tc := range cases {
		start, end, ok, err := parseVideoSegment(tc.in)
		if err != nil {
			t.Errorf("parseVideoSegment(%q): %v", tc.in, err)
			continue
		}
		if start != tc.start || end != tc.end || ok != tc.ok {
			t.Errorf("parseVideoSegment(%q) = %g, %g, %v; want %g, %g, %v",
				tc.in, start, end, ok, tc.start, tc.end, tc.ok)
		}
	}
}

func TestParseVideoSegmentRejects(t *testing.T) {
	for _, in := range []string{
		"04:10",       // no end
		"04:40-04:10", // backwards
		"04:10-04:10", // empty window
		"junk-junk",   // not a timestamp
		"-04:40",      // missing start
		"04:10-",      // missing end
		"04:10--04:40",
	} {
		if _, _, _, err := parseVideoSegment(in); err == nil {
			t.Errorf("parseVideoSegment(%q) accepted, want an error", in)
		}
	}
}

// TestExtractVideoFramesFocusesSegment pins the overview-plus-detail shape on
// real footage: a flash of change inside the named window is sampled densely
// there, the overview keeps the rest of the recording and leaves the window to
// the dense pass, and the manifest says which frames the detail pass kept.
func TestExtractVideoFramesFocusesSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := t.TempDir()
	videoPath := filepath.Join(out, "focus.mp4")
	gen := exec.Command("ffmpeg", "-y", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=0x111111:s=640x360:d=6:rate=10",
		"-vf", "drawbox=x=300:y=200:w=200:h=60:color=0x2a5caa@1:t=fill:enable='between(t,2.4,2.7)'",
		"-pix_fmt", "yuv420p", videoPath)
	if genOut, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, genOut)
	}

	manifest, err := ExtractVideoFrames(context.Background(), VideoExtractOptions{
		Path:         videoPath,
		OutDir:       out,
		SampleFPS:    1,
		SegmentStart: 2,
		SegmentEnd:   3,
		FocusFrames:  20,
	})
	if err != nil {
		t.Fatalf("ExtractVideoFrames: %v", err)
	}
	if manifest.SegmentStartSec != 2 || manifest.SegmentEndSec != 3 {
		t.Fatalf("manifest window = %g–%g, want 2–3", manifest.SegmentStartSec, manifest.SegmentEndSec)
	}
	// 20 detail frames asked for across a one-second window would be 20 fps,
	// capped at the focus rate.
	if manifest.FocusFPS != videoFocusMaxFPS {
		t.Errorf("manifest.FocusFPS = %g, want %g", manifest.FocusFPS, videoFocusMaxFPS)
	}

	focus := 0
	prev := -1.0
	for _, frame := range manifest.Frames {
		if frame.Seconds <= prev {
			t.Fatalf("frames are not chronological around %s", frame.Label)
		}
		prev = frame.Seconds
		if frame.Focus {
			focus++
			if frame.Seconds < 2 || frame.Seconds > 3 {
				t.Errorf("focus frame outside the window: %s", frame.Label)
			}
			continue
		}
		if frame.Seconds >= 2 && frame.Seconds < 3 {
			t.Errorf("the overview kept a frame inside the focus window: %s", frame.Label)
		}
	}
	if focus == 0 {
		t.Fatalf("the focus pass kept nothing (kept %d, dropped %d, blur %d)",
			manifest.Kept, manifest.Dropped, manifest.BlurDropped)
	}
	if manifest.FocusKept != focus {
		t.Errorf("manifest.FocusKept = %d, frames marked focus %d", manifest.FocusKept, focus)
	}
}

// TestExtractVideoFramesSegmentPastEnd pins the mis-aimed window: a segment
// starting after the recording ends is an error naming both timestamps.
func TestExtractVideoFramesSegmentPastEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := t.TempDir()
	videoPath := filepath.Join(out, "short.mp4")
	gen := exec.Command("ffmpeg", "-y", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=0x111111:s=320x240:d=2:rate=10",
		"-pix_fmt", "yuv420p", videoPath)
	if genOut, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, genOut)
	}

	_, err := ExtractVideoFrames(context.Background(), VideoExtractOptions{
		Path:         videoPath,
		OutDir:       out,
		SegmentStart: 10,
		SegmentEnd:   12,
	})
	if err == nil {
		t.Fatal("a segment past the end of the recording was accepted")
	}
	if !strings.Contains(err.Error(), "past the end") {
		t.Errorf("err = %v, want the past-the-end message", err)
	}
}
