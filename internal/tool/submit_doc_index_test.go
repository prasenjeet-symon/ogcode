package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// The label ceiling is advertised to the model as maxItems in the schema, and
// the constant is what the code enforces. If the two drift, the model is told a
// limit the tool does not have (or worse, the reverse) — the exact class of
// contradiction that had the prompt asking for "4-8" while the tool description
// said "2-5".
func TestSubmitDocIndex_LabelCeilingMatchesSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Pages struct {
				Items struct {
					Properties struct {
						Labels struct {
							MaxItems *int `json:"maxItems"`
						} `json:"labels"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"pages"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SubmitDocIndexTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	got := schema.Properties.Pages.Items.Properties.Labels.MaxItems
	if got == nil {
		t.Fatalf("labels has no maxItems — the ceiling is not advertised to the model")
	}
	if *got != MaxLabelsPerPage {
		t.Errorf("schema maxItems = %d, want MaxLabelsPerPage (%d)", *got, MaxLabelsPerPage)
	}
}

// Every label handed to the tool is stored — the tool is not a second, quieter
// cap beneath the schema's. A model that supplies labels up to the ceiling must
// find all of them on the row.
func TestSubmitDocIndex_StoresEveryLabel(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := docindex.NewStore(database)
	tool := NewSubmitDocIndexTool(store)

	labels := make([]string, MaxLabelsPerPage)
	for i := range labels {
		labels[i] = string(rune('a' + i%26))
	}

	args, _ := json.Marshal(map[string]any{
		"doc_path": "/proj/a.go",
		"pages":    []map[string]any{{"page_num": 1, "labels": labels}},
	})
	if _, err := tool.Execute(context.Background(), args, Context{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries, err := store.GetByDoc("/proj/a.go")
	if err != nil {
		t.Fatalf("get by doc: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 page entry, got %d", len(entries))
	}
	if len(entries[0].Labels) != len(labels) {
		t.Errorf("stored %d labels, want all %d — the tool dropped labels the schema allowed",
			len(entries[0].Labels), len(labels))
	}
}
