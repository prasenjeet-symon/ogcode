package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingRecall captures what project_memory_recall hands to the recall
// runner, pinning the scope/target contract at the delegation boundary.
type recordingRecall struct {
	calls           int
	question        string
	scope           string
	targetSessionID string
}

func (r *recordingRecall) Recall(ctx context.Context, question, scope, targetSessionID, dir, model string) (string, error) {
	r.calls++
	r.question = question
	r.scope = scope
	r.targetSessionID = targetSessionID
	return "synthesized answer", nil
}

func projectRecallContext(t *testing.T) Context {
	t.Helper()
	return Context{SessionID: "ses_fixture", SessionDir: t.TempDir()}
}

func projectRecallArgs(t *testing.T, args string) json.RawMessage {
	t.Helper()
	if args == "" {
		return nil
	}
	return json.RawMessage(args)
}

// TestProjectMemoryRecall_AlwaysProjectScope pins the post-scope-removal
// contract: the tool has no scope parameter, always delegates with scope
// "project" and no target session, and passes the question through verbatim.
// Per-conversation recall is memory_recall's job, not a parameter on this tool.
func TestProjectMemoryRecall_AlwaysDelegatesProjectScope(t *testing.T) {
	rec := &recordingRecall{}
	res, err := NewProjectMemoryRecallTool(rec.Recall, nil).Execute(
		context.Background(),
		projectRecallArgs(t, `{"question":"why is compaction budgeted per run"}`),
		projectRecallContext(t))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("expected 1 recall delegation, got %d", rec.calls)
	}
	if rec.question != "why is compaction budgeted per run" {
		t.Errorf("question not passed through verbatim: %q", rec.question)
	}
	if rec.scope != "project" {
		t.Errorf("expected scope \"project\", got %q", rec.scope)
	}
	if rec.targetSessionID != "" {
		t.Errorf("expected empty target session, got %q", rec.targetSessionID)
	}
	if res.Title != "Project Memory Recall" {
		t.Errorf("expected title \"Project Memory Recall\", got %q", res.Title)
	}
	if !strings.Contains(res.Output, "synthesized answer") {
		t.Errorf("expected the recall answer in output, got %q", res.Output)
	}
}

// TestProjectMemoryRecall_ScopeArgumentRejected pins that a scope the model
// still sends (older habit) fails cleanly with guidance instead of narrowing
// the search. Any value — including the legacy "project" and "session" — is
// refused, because the schema no longer advertises the parameter at all.
func TestProjectMemoryRecall_ScopeArgumentRejected(t *testing.T) {
	for _, scope := range []string{"session", "project", "bogus"} {
		rec := &recordingRecall{}
		payload, err := json.Marshal(map[string]any{"question": "q", "scope": scope})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		res, execErr := NewProjectMemoryRecallTool(rec.Recall, nil).Execute(
			context.Background(), payload, projectRecallContext(t))
		if execErr != nil {
			t.Fatalf("scope %q: Execute returned error: %v", scope, execErr)
		}
		if rec.calls != 0 {
			t.Errorf("scope %q: recall must not run, got %d calls", scope, rec.calls)
		}
		if !strings.Contains(res.Output, "no scope parameter") {
			t.Errorf("scope %q: expected guidance in output, got %q", scope, res.Output)
		}
		if !strings.Contains(res.Output, "memory_recall") {
			t.Errorf("scope %q: guidance must point at memory_recall, got %q", scope, res.Output)
		}
	}
}

// TestProjectMemoryRecall_NoQuestionAndNoRecall covers the early returns.
func TestProjectMemoryRecall_NoQuestionAndNoRecall(t *testing.T) {
	rec := &recordingRecall{}
	tool := NewProjectMemoryRecallTool(rec.Recall, nil)

	res, err := tool.Execute(context.Background(), projectRecallArgs(t, `{}`), projectRecallContext(t))
	if err != nil {
		t.Fatalf("empty args: Execute returned error: %v", err)
	}
	if !strings.Contains(res.Output, "No question provided") {
		t.Errorf("empty args: unexpected output %q", res.Output)
	}

	res, err = NewProjectMemoryRecallTool(nil, nil).Execute(
		context.Background(), projectRecallArgs(t, `{"question":"q"}`), projectRecallContext(t))
	if err != nil {
		t.Fatalf("nil recall: Execute returned error: %v", err)
	}
	if !strings.Contains(res.Output, "Memory is not enabled") {
		t.Errorf("nil recall: unexpected output %q", res.Output)
	}
}

// TestProjectMemoryRecall_SchemaHasNoScope pins the schema: the scope property
// must stay gone and the description must route per-conversation questions to
// memory_recall by name, so the model learns the division of labor from the
// tool surface itself.
func TestProjectMemoryRecall_SchemaHasNoScope(t *testing.T) {
	tool := NewProjectMemoryRecallTool(nil, nil)

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("schema does not parse: %v", err)
	}
	if _, present := schema.Properties["scope"]; present {
		t.Error("schema still declares a scope property")
	}
	if _, present := schema.Properties["question"]; !present {
		t.Error("schema lost the question property")
	}

	desc := tool.Description()
	if !strings.Contains(desc, "memory_recall") {
		t.Error("description does not route per-conversation recall to memory_recall")
	}
	if strings.Contains(desc, "scope") {
		t.Error("description still mentions the removed scope parameter")
	}
}
