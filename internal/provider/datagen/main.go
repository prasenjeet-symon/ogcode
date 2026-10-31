// Command datagen regenerates internal/provider/data/catalog.json from the
// compiled-in catalog tables. Run from the repo root:
//
//	CGO_ENABLED=1 go run ./internal/provider/datagen
//
// The generated file is the tracked upstream source the SyncUpstreamCatalog
// machinery fetches and merges; keep it in sync with the compiled-in tables
// whenever they change.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// collectionFor is the list each compiled-in table backs, using the same
// collection labels UpstreamCatalog merging places new rows by.
type collectionFor struct {
	label string
	list  []provider.CatalogModel
}

func main() {
	lists := []collectionFor{
		{"anthropic", provider.AnthropicModels},
		{"openai", provider.OpenAIModels},
		{"google", provider.GoogleModels},
		{"open", provider.OpenModels},
		{"legacy", provider.LegacyModels},
	}

	models := make([]provider.CatalogModel, 0)
	for _, l := range lists {
		for _, m := range l.list {
			m.Collection = l.label
			models = append(models, m)
		}
	}

	page := struct {
		Version     int                     `json:"version"`
		GeneratedAt time.Time               `json:"generatedAt"`
		Models      []provider.CatalogModel `json:"models"`
	}{
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		Models:      models,
	}

	data, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		fatal(err)
	}
	data = append(data, '\n')

	out := filepath.Join("internal", "provider", "data", "catalog.json")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: %d models\n", out, len(models))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
