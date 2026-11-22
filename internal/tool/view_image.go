package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register GIF decoder
	"image/jpeg"
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp" // register BMP decoder
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder
)

// viewMaxLongEdgePx caps the long edge of returned images. Vision models
// resize images above this threshold anyway (Anthropic's limit is ~1568px),
// so we downscale proactively to keep base64 payload sizes reasonable.
const viewMaxLongEdgePx = 1568

// Pixel-cap map (informational only — the caps below are unchanged):
// attachments the user sends from the web UI are downscaled to a ~1024px
// long edge (web/src/lib/images.ts MAX_ATTACHMENT_LONG_EDGE, ~0.79 MP for
// 4:3) to cut billed tokens; the backend caps view_image/analyze_image at
// viewMaxLongEdgePx (1568px, ~Anthropic's ceiling); PDF page renders use
// renderMaxLongEdgePx (1400px in pdf_render.go). Backend and frontend caps
// differ on purpose — each side sizes for its own payload — so do not
// "fix" the mismatch by changing any of them.
const viewAnimatedNote = "note: only the first frame of an animated image is analyzed"

// ViewImageTool lets the agent view an image file (PNG, JPEG, GIF, BMP, WebP)
// on disk. For vision-capable models the decoded image is returned as a
// ResultImage; otherwise a textual description is returned.
type ViewImageTool struct{}

func (ViewImageTool) ID() string { return "view_image" }

func (ViewImageTool) Description() string {
	return "Read an image file (PNG, JPEG, GIF, BMP, or WebP) from disk and return it so the model can see it. Use this when you need to visually inspect an image — a logo, screenshot, diagram, photo, etc. The path may be absolute or relative to the session directory. Large images are automatically downscaled to fit within vision-model limits."
}

func (ViewImageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the image file (absolute or relative to session directory)"
			}
		}
	}`)
}

func (ViewImageTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse view_image args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "View Image", Output: "path is required"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("open image %s: %w", path, err)
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("decode image %s: %w (supported formats: png, jpeg, gif, bmp, webp)", path, err)
	}
	// image.Decode ignores EXIF orientation, so a phone photo shot in
	// portrait arrives sideways. The browser frontend applies the tag via
	// imageOrientation: 'from-image'; rotate here so backend and frontend
	// hand the model the same upright pixels.
	img = applyExifOrientation(img, exifOrientation(raw))

	bounds := boundsStr(img)
	title := fmt.Sprintf("View Image: %s", filepath.Base(path))

	if !tctx.ModelSupportsImages {
		return Result{
			Title:  title,
			Output: fmt.Sprintf("Image found at %s (%s, %s) but the current model does not support image input. Use analyze_image instead to transcribe, describe, or answer questions about it using a vision-capable model.", params.Path, bounds, format),
		}, nil
	}

	data, mediaType, err := encodeImageForModel(img)
	if err != nil {
		return Result{}, fmt.Errorf("encode image %s: %w", path, err)
	}

	return Result{
		Title:  title,
		Output: viewImageOutput(params.Path, bounds, format),
		Image: &ResultImage{
			MediaType: mediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

// boundsStr returns a human-readable "WxH" string for an image.
func boundsStr(img image.Image) string {
	b := img.Bounds()
	return fmt.Sprintf("%dx%d", b.Dx(), b.Dy())
}

// viewImageOutput renders the attached-image line, noting for animated
// formats (GIF carries multiple frames; image.Decode returns the first) that
// only the first frame reaches the model — the frontend deliberately passes
// GIFs through, so the warning belongs on both sides.
func viewImageOutput(path, bounds, format string) string {
	out := fmt.Sprintf("Image %s (%s, %s) is attached.", path, bounds, format)
	if format == "gif" {
		out += " " + viewAnimatedNote + "."
	}
	return out
}

// encodeImageForModel re-encodes the decoded image as a JPEG (quality 80),
// downscaling it if the long edge exceeds viewMaxLongEdgePx. This keeps
// payload sizes reasonable while staying within vision-model limits.
// Alpha is composited onto opaque white first: encoding an NRGBA straight
// to JPEG drops the alpha channel, and the zero-valued (transparent black)
// background shows through as black. White matches a document page and what
// the web frontend shows for transparent attachments it re-encodes.
func encodeImageForModel(img image.Image) ([]byte, string, error) {
	b := img.Bounds()
	longEdge := b.Dx()
	if b.Dy() > longEdge {
		longEdge = b.Dy()
	}

	// Downscale if the image is larger than the cap.
	if longEdge > viewMaxLongEdgePx {
		scale := float64(viewMaxLongEdgePx) / float64(longEdge)
		newW := int(float64(b.Dx()) * scale)
		newH := int(float64(b.Dy()) * scale)
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		draw.BiLinear.Scale(dst, dst.Bounds(), flattenImage(img), flattenRect(b), draw.Src, nil)
		img = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flattenImage(img), &jpeg.Options{Quality: renderJPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("encode to jpeg: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}

// flattenRect returns the source rectangle for a pre-flatten draw scaled
// into a fresh RGBA: flattenImage shifts non-zero origins to (0,0), so the
// source rect must be rebased the same way.
func flattenRect(b image.Rectangle) image.Rectangle {
	return image.Rect(0, 0, b.Dx(), b.Dy())
}

// flattenImage composites img onto an opaque white canvas. JPEG has no
// alpha channel, so encoding a translucent PNG or WebP directly turns every
// transparent pixel black; white preserves text-on-transparent screenshots
// and matches the frontend's rendering of transparent attachments.
func flattenImage(img image.Image) image.Image {
	if opaqueImage(img) {
		return img
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	white := image.NewUniform(color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	draw.Draw(dst, dst.Bounds(), white, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// opaqueImage reports whether img carries no alpha worth preserving: an
// *image.Opaque fast path plus the NRGBA/RGBA checks the encoders use, so
// fully-opaque pictures skip the extra white-background copy.
func opaqueImage(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return true
	}
	b := img.Bounds()
	switch p := img.(type) {
	case *image.NRGBA:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if p.NRGBAAt(x, y).A != 0xff {
					return false
				}
			}
		}
		return true
	case *image.RGBA:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if p.RGBAAt(x, y).A != 0xff {
					return false
				}
			}
		}
		return true
	}
	return false
}

// exifOrientation reads the EXIF orientation tag (1-8) from raw JPEG bytes,
// returning 1 (no transform) when the file is not a JPEG, carries no EXIF,
// or the tag cannot be parsed. Only the orientation tag is read — the file
// is never fully decoded here — so corrupt metadata degrades to upright
// rather than failing the view.
func exifOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return 1 // not a JPEG: no EXIF segment to read
	}
	off := 2
	for off+4 <= len(raw) {
		if raw[off] != 0xff {
			return 1
		}
		marker := raw[off+1]
		if marker == 0xda || marker == 0xd9 {
			return 1 // start of scan / end of image: no metadata past here
		}
		if off+4 > len(raw) {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(raw[off+2:]))
		if segLen < 2 || off+2+segLen > len(raw) {
			return 1
		}
		// APP1 carrying the "Exif\x00\x00" header holds the TIFF tags.
		if marker == 0xe1 && segLen >= 8 && string(raw[off+4:off+10]) == "Exif\x00\x00" {
			if orient := exifTIFFOrientation(raw[off+10 : off+2+segLen]); orient >= 1 && orient <= 8 {
				return orient
			}
			return 1
		}
		off += 2 + segLen
	}
	return 1
}

// exifTIFFOrientation parses the TIFF header inside an EXIF APP1 segment
// and returns the Orientation tag (0x0112), or 0 when it is absent or the
// header is malformed. Both byte orders are handled; only the first IFD is
// read — the orientation tag lives there — and every offset is bounds
// checked so a truncated file degrades to upright instead of panicking.
func exifTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:4]) {
	case "II\x2a\x00":
		order = binary.LittleEndian
	case "MM\x00\x2a":
		order = binary.BigEndian
	default:
		return 0
	}
	ifdOff := int(order.Uint32(tiff[4:8]))
	if ifdOff < 0 || ifdOff+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[ifdOff:]))
	for i := 0; i < count; i++ {
		entry := ifdOff + 2 + i*12
		if entry+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[entry:]) != 0x0112 {
			continue
		}
		// Orientation is a SHORT (type 3) with count 1; the value sits in
		// the first two bytes of the value field (endian per the header).
		if order.Uint16(tiff[entry+2:]) != 3 || order.Uint32(tiff[entry+4:]) != 1 {
			return 0
		}
		return int(order.Uint16(tiff[entry+8:]))
	}
	return 0
}

// applyExifOrientation rotates/flips img per the EXIF orientation value so
// the returned pixels are upright. Values 1 (or anything out of range) are
// returned unchanged; the rest mirror the EXIF spec (2/4/5/7 are mirrors,
// 3/6/8 are 180/90/270-degree clockwise rotations).
func applyExifOrientation(img image.Image, orient int) image.Image {
	switch orient {
	case 2, 3, 4, 5, 6, 7, 8:
	default:
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := orient >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orient {
			case 2: // mirror horizontal
				sx, sy = w-1-x, y
			case 3: // rotate 180
				sx, sy = w-1-x, h-1-y
			case 4: // mirror vertical
				sx, sy = x, h-1-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 CW
				sx, sy = y, h-1-x
			case 7: // transverse
				sx, sy = w-1-y, h-1-x
			case 8: // rotate 270 CW
				sx, sy = w-1-y, x
			}
			dst.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA))
		}
	}
	return dst
}

// Compile-time guard: ViewImageTool satisfies the ToolDef interface.
var _ ToolDef = ViewImageTool{}
