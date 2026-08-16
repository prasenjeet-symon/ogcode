// Package embedmodel bundles a sentence-embedding model so agentic memory
// works with zero external dependencies — no API key, no network call (after
// the one-time model download), no separate model server required.
//
// The model is thenlper/gte-small (384-dimensional embeddings), run via Hugot's
// pure-Go (GoMLX simplego) backend. Because Hugot loads models from filesystem
// paths, the small tokenizer/config assets are embedded in the binary and
// lazily materialized to a cache directory on first use, while the large ONNX
// weight file (~133 MB) is downloaded on first use from Hugging Face rather
// than bloating the binary. This mirrors ogcode's existing search-bridge
// download pattern and keeps the distributable binary small while preserving
// the single-command, no-API-key experience.
//
// gte-small was chosen over the previous all-MiniLM-L6-v2 because it scores
// higher on the MTEB benchmark (~61.4 vs ~56.3), requires no query/passage
// prefixing (unlike bge-small or e5-small), and is a drop-in 384-dim replacement.
package embedmodel

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var assets embed.FS

// AssetNames is the ordered list of small files bundled in the binary, relative
// to the assets/ directory. Each is written verbatim to the cache dir. The
// large ONNX model file is NOT in this list — it is downloaded on first use
// (see ModelURL / ModelSHA256).
var AssetNames = []string{
	"tokenizer.json",
	"tokenizer_config.json",
	"special_tokens_map.json",
	"config.json",
	"vocab.txt",
}

// ModelName is the human-readable identifier of the bundled model.
const ModelName = "thenlper/gte-small"

// ModelFileName is the name of the ONNX weight file in the cache directory.
const ModelFileName = "model.onnx"

// ModelURL is the canonical download URL for the ONNX weights. The file is
// fetched on first use when it is not already present in the cache directory.
const ModelURL = "https://huggingface.co/thenlper/gte-small/resolve/main/onnx/model.onnx"

// ModelSHA256 is the expected SHA-256 of the downloaded ONNX file, used to
// verify integrity and detect partial/corrupt downloads.
const ModelSHA256 = "0b01312b59bec0a2558a626f2937be4cbe4bb16d1511560153f598cec488f1f8"

// EmbeddingDim is the dimensionality of the vectors produced by the model.
const EmbeddingDim = 384

// ReadAsset returns the raw bytes of a bundled asset file. name must be one of
// AssetNames. It panics if the asset is missing (a build-time packaging bug).
func ReadAsset(name string) []byte {
	b, err := fs.ReadFile(assets, "assets/"+name)
	if err != nil {
		panic("embedmodel: missing asset " + name + ": " + err.Error())
	}
	return b
}