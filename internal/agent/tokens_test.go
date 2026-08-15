package agent

import (
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

func TestEstimateTokens_Empty(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("estimateTokens(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokens_ProseNotOverCounted(t *testing.T) {
	// Prose should stay close to the ~4-chars-per-token rule, not blow up.
	prose := "The quick brown fox jumps over the lazy dog."
	byteEst := len(prose) / 4
	got := estimateTokens(prose)
	if got > byteEst*3/2 {
		t.Errorf("prose over-counted: estimateTokens=%d, byte estimate=%d (want <= 1.5x)", got, byteEst)
	}
	if got < byteEst/2 {
		t.Errorf("prose under-counted: estimateTokens=%d, byte estimate=%d", got, byteEst)
	}
}

func TestEstimateTokens_DenseJSONNotUnderCounted(t *testing.T) {
	// The whole point of the heuristic: punctuation-dense JSON must NOT collapse
	// to the bytes/4 estimate (which under-counts it ~2-3x).
	json := `{"name":"test","values":[1,2,3],"nested":{"a":true,"b":false},"id":42}`
	byteEst := len(json) / 4
	got := estimateTokens(json)
	if got <= byteEst {
		t.Errorf("dense JSON not lifted above the byte estimate: estimateTokens=%d, byte estimate=%d", got, byteEst)
	}
}

func TestEstimateRequestTokens_CountsToolSchemas(t *testing.T) {
	bigSchema := `{"type":"object","properties":{` +
		strings.Repeat(`"field_`, 50) + `":{"type":"string","description":"a field"},` +
		`},"required":["a","b","c"]}`

	base := provider.StreamRequest{
		System:   []string{"you are a helpful assistant"},
		Messages: []provider.ModelMessage{{Role: "user", Content: []byte(`"hello"`)}},
	}
	withTools := base
	withTools.Tools = []provider.ToolDefinition{
		{Name: "bash", Description: "run a shell command", Parameters: []byte(bigSchema)},
	}

	withoutT := estimateRequestTokens(base)
	withT := estimateRequestTokens(withTools)
	delta := withT - withoutT
	// The tool schema is large; its tokens must be reflected in the estimate (the
	// old byte estimator skipped tools entirely).
	if delta < estimateTokens(bigSchema)/2 {
		t.Errorf("tool schema under-counted: delta=%d, schema estimate=%d", delta, estimateTokens(bigSchema))
	}
}

func TestEstimateRequestTokens_ImageUsesFlatCost(t *testing.T) {
	// A megabyte-scale base64 image must be counted at the flat per-image cost,
	// not as ~len/4 text tokens (which over-counted it by ~100x and would trip
	// compaction spuriously).
	bigB64 := strings.Repeat("QUJD", 25_000) // 100k base64 chars
	req := provider.StreamRequest{
		Messages: []provider.ModelMessage{{
			Role:   "user",
			Images: []provider.MessageImage{{MediaType: "image/png", Data: bigB64}},
		}},
	}
	got := estimateRequestTokens(req)
	if got > imageTokenEstimate*2 {
		t.Errorf("image counted as text: estimateRequestTokens=%d, want ~%d (flat)", got, imageTokenEstimate)
	}
	if got < imageTokenEstimate {
		t.Errorf("image cost not counted: got %d, want >= %d", got, imageTokenEstimate)
	}
}
