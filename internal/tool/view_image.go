package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
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

	f, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open image %s: %w", path, err)
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		return Result{}, fmt.Errorf("decode image %s: %w (supported formats: png, jpeg, gif, bmp, webp)", path, err)
	}

	bounds := boundsStr(img)
	title := fmt.Sprintf("View Image: %s", filepath.Base(path))

	if !tctx.ModelSupportsImages {
		return Result{
			Title:  title,
			Output: fmt.Sprintf("Image found at %s (%s, %s) but the current model does not support image input.", params.Path, bounds, format),
		}, nil
	}

	data, mediaType, err := encodeImageForModel(img)
	if err != nil {
		return Result{}, fmt.Errorf("encode image %s: %w", path, err)
	}

	return Result{
		Title:  title,
		Output: fmt.Sprintf("Image %s (%s, %s) is attached.", params.Path, bounds, format),
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

// encodeImageForModel re-encodes the decoded image as a JPEG (quality 80),
// downscaling it if the long edge exceeds viewMaxLongEdgePx. This keeps
// payload sizes reasonable while staying within vision-model limits.
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
		draw.BiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: renderJPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("encode to jpeg: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}

// Compile-time guard: ViewImageTool satisfies the ToolDef interface.
var _ ToolDef = ViewImageTool{}
