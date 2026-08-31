package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
)

// imageRejectionHints are substrings that, when present in a provider error,
// indicate the model definitively does not accept image input (as opposed to an
// unrelated failure like auth or rate limiting).
var imageRejectionHints = []string{
	"image", "multimodal", "modalit", "vision", "no images", "not support image",
}

var tinyImageOnce sync.Once
var tinyImageB64 string

// tinyImage returns a base64-encoded minimal JPEG used for capability probing.
func tinyImage() string {
	tinyImageOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for x := 0; x < 4; x++ {
			for y := 0; y < 4; y++ {
				img.Set(x, y, color.White)
			}
		}
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60})
		tinyImageB64 = base64.StdEncoding.EncodeToString(buf.Bytes())
	})
	return tinyImageB64
}

// ProbeImageSupport sends a single minimal image to the model and reports whether
// it was accepted. Return values:
//   - (true, true, nil):   the model accepted the image and responded.
//   - (false, true, nil):  the provider rejected the request for an image/modality reason.
//   - (false, false, err): inconclusive (network/auth/rate-limit/etc.) — do NOT cache; retry later.
func ProbeImageSupport(ctx context.Context, p Provider, modelID string) (supports bool, definitive bool, err error) {
	textJSON, _ := json.Marshal("Reply with: ok")
	req := StreamRequest{
		Model: modelID,
		Messages: []ModelMessage{{
			Role:    "user",
			Content: textJSON,
			Images:  []MessageImage{{MediaType: "image/jpeg", Data: tinyImage()}},
		}},
		MaxTokens: 16,
	}

	ch, startErr := p.StreamChat(ctx, req)
	if startErr != nil {
		return classifyProbeError(startErr.Error())
	}

	var streamErr string
	gotOutput := false
	for ev := range ch {
		switch ev.Type {
		case EventError:
			streamErr = ev.Error
		case EventTextDelta, EventFinish:
			gotOutput = true
		}
	}

	if streamErr != "" {
		return classifyProbeError(streamErr)
	}
	if gotOutput {
		return true, true, nil
	}
	// No output and no error — treat as inconclusive rather than guessing.
	return false, false, nil
}

// classifyProbeError maps a provider error string to a probe result. An
// image/modality-related error is a definitive "no"; anything else is
// inconclusive so the caller falls back to a heuristic without caching.
func classifyProbeError(errStr string) (bool, bool, error) {
	if IsImageRejectionMessage(errStr) {
		return false, true, nil
	}
	return false, false, &probeError{msg: errStr}
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }
