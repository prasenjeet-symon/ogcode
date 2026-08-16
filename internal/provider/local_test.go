package provider

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider/embedmodel"
)

// setCacheDir returns a temp directory and sets OGCODE_EMBED_MODEL_DIR so that
// NewLocalEmbedder("") materializes the model there. It restores the previous
// value on cleanup.
func setCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, hadPrev := os.LookupEnv("OGCODE_EMBED_MODEL_DIR")
	os.Setenv("OGCODE_EMBED_MODEL_DIR", dir)
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("OGCODE_EMBED_MODEL_DIR", prev)
		} else {
			os.Unsetenv("OGCODE_EMBED_MODEL_DIR")
		}
	})
	return dir
}

// TestLocalEmbedderEmbedModelAndID checks the static identifiers exposed by the
// embedder. No model download or inference is required.
func TestLocalEmbedderEmbedModelAndID(t *testing.T) {
	e := NewLocalEmbedder("")
	if got := e.EmbedModel(); got != embedmodel.ModelName {
		t.Fatalf("EmbedModel() = %q, want %q", got, embedmodel.ModelName)
	}
	if got := e.ID(); got != localProviderID {
		t.Fatalf("ID() = %q, want %q", got, localProviderID)
	}
	if e.Models() != nil {
		t.Fatalf("Models() should be nil for embedding-only provider")
	}
	if _, err := e.StreamChat(context.Background(), StreamRequest{}); err == nil {
		t.Fatal("StreamChat on an embedder must return an error")
	}
}

// TestLocalEmbedderCacheDirEnvOverride verifies that NewLocalEmbedder("") honors
// the OGCODE_EMBED_MODEL_DIR environment variable.
func TestLocalEmbedderCacheDirEnvOverride(t *testing.T) {
	dir := setCacheDir(t)
	e := NewLocalEmbedder("")
	if e.baseDir != dir {
		t.Fatalf("baseDir = %q, want %q (from OGCODE_EMBED_MODEL_DIR)", e.baseDir, dir)
	}
}

// TestLocalEmbedderExplicitCacheDir verifies that a non-empty cacheDir argument
// takes precedence over the environment variable.
func TestLocalEmbedderExplicitCacheDir(t *testing.T) {
	setCacheDir(t) // sets env, but explicit arg must win
	explicit := t.TempDir()
	e := NewLocalEmbedder(explicit)
	if e.baseDir != explicit {
		t.Fatalf("baseDir = %q, want explicit %q", e.baseDir, explicit)
	}
}

// TestLocalEmbedderPrepareModelCachedShortCircuit verifies that when the model
// file and a valid SHA-256 marker are already present, prepareModel materializes
// the tokenizer assets and returns without attempting a network download.
func TestLocalEmbedderPrepareModelCachedShortCircuit(t *testing.T) {
	dir := setCacheDir(t)
	e := NewLocalEmbedder("")

	// Simulate a previously-verified download: write a placeholder model file and
	// the marker with the expected hash.
	modelPath := filepath.Join(dir, embedmodel.ModelFileName)
	if err := os.WriteFile(modelPath, []byte("FAKE-WEIGHTS"), 0o644); err != nil {
		t.Fatalf("seed model file: %v", err)
	}
	markerPath := filepath.Join(dir, ".ogcode-model.sha256")
	if err := os.WriteFile(markerPath, []byte(embedmodel.ModelSHA256), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	if err := e.prepareModel(context.Background()); err != nil {
		t.Fatalf("prepareModel with cached model returned error: %v", err)
	}

	// The model file must be untouched (no re-download/rename).
	got, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read model after prepareModel: %v", err)
	}
	if string(got) != "FAKE-WEIGHTS" {
		t.Fatalf("cached model was overwritten: got %q", string(got))
	}

	// All embedded tokenizer assets must have been materialized.
	for _, name := range embedmodel.AssetNames {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("asset %q not materialized: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("asset %q is empty", name)
		}
	}
}

// TestLocalEmbedderEnsureModelDownloadedCached verifies the public preflight
// helper returns nil without a network call when the cache is warm, and that the
// underlying LocalEmbedder can then run Embed against the (fake) cache without
// re-downloading — but only the download path is exercised here; inference would
// fail on fake weights, so we stop at prepareModel via EnsureModelDownloaded.
func TestLocalEmbedderEnsureModelDownloadedCached(t *testing.T) {
	dir := setCacheDir(t)
	if err := os.WriteFile(filepath.Join(dir, embedmodel.ModelFileName), []byte("FAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ogcode-model.sha256"), []byte(embedmodel.ModelSHA256), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLocalEmbedderModel(context.Background()); err != nil {
		t.Fatalf("EnsureLocalEmbedderModel with warm cache: %v", err)
	}
}

// --- Integration test: downloads the real ~133 MB model and runs inference. ---
//
// This is gated behind OGCODE_EMBED_INTEGRATION=1 so the default `go test ./...`
// (including CI) stays fast and offline. To run it locally:
//
//	OGCODE_EMBED_INTEGRATION=1 go test ./internal/provider/ -run TestLocalEmbedderIntegration -v -timeout 10m
func TestLocalEmbedderIntegration(t *testing.T) {
	if os.Getenv("OGCODE_EMBED_INTEGRATION") != "1" {
		t.Skip("set OGCODE_EMBED_INTEGRATION=1 to run the local embedder integration test (downloads ~133 MB model)")
	}

	dir := setCacheDir(t)
	e := NewLocalEmbedder("")
	defer e.Close()

	ctx := context.Background()

	// First call triggers the one-time download + pipeline build.
	sentences := []string{
		"I love dogs and puppies.",
		"Puppies are great pets.",
		"The weather is sunny today.",
	}
	vecs, err := e.Embed(ctx, sentences)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vecs) != len(sentences) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(sentences))
	}
	for i, v := range vecs {
		if len(v) != embedmodel.EmbeddingDim {
			t.Fatalf("vec[%d] dim = %d, want %d", i, len(v), embedmodel.EmbeddingDim)
		}
		if norm(v) == 0 {
			t.Fatalf("vec[%d] has zero norm", i)
		}
	}

	// Semantic sanity: related sentences should be more similar than unrelated
	// ones. A meaningful embedding model puts "I love dogs" closer to "puppies
	// are great" than to "the weather is sunny".
	sim := cosineLocal(vecs[0], vecs[1])
	unrelated := cosineLocal(vecs[0], vecs[2])
	if sim <= unrelated {
		t.Fatalf("semantic similarity check failed: sim(related)=%f <= sim(unrelated)=%f", sim, unrelated)
	}
	t.Logf("cosine(related)=%.4f cosine(unrelated)=%.4f", sim, unrelated)

	// The model must now be cached on disk for subsequent calls.
	if _, err := os.Stat(filepath.Join(dir, embedmodel.ModelFileName)); err != nil {
		t.Fatalf("model not persisted to cache dir: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".ogcode-model.sha256"))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if string(marker) != embedmodel.ModelSHA256 {
		t.Fatalf("marker = %q, want %q", string(marker), embedmodel.ModelSHA256)
	}

	// A fresh instance sharing the cache must NOT re-download: it should still
	// produce embeddings and the model file must be unchanged.
	e2 := NewLocalEmbedder("")
	defer e2.Close()
	if _, err := e2.Embed(ctx, []string{"hello world"}); err != nil {
		t.Fatalf("second instance Embed failed: %v", err)
	}
}

func norm(v []float32) float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return float32(math.Sqrt(s))
}

// cosine is duplicated from the memory package for the integration test's
// semantic check. Kept local to this test file to avoid an import cycle.
func cosineLocal(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float32
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
			magB += b[i] * b[i]
		}
		magA += a[i] * a[i]
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	sqrt := func(f float32) float32 { return float32(math.Sqrt(float64(f))) }
	return dot / (sqrt(magA) * sqrt(magB))
}