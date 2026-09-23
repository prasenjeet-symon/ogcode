package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/question"
)

func askUserArgs(t *testing.T, qs ...question.Question) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]any{"questions": qs})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return data
}

func TestAskUserTool_ReturnsTheAnswers(t *testing.T) {
	var gotQuestions []question.Question
	var gotSession string
	tool := AskUserTool{Ask: func(_ context.Context, sessionID string, qs []question.Question) (question.Reply, error) {
		gotSession = sessionID
		gotQuestions = qs
		return question.Reply{Answers: []question.Answer{{Selected: []string{"Postgres"}, Text: "and a pooler"}}}, nil
	}}

	res, err := tool.Execute(context.Background(), askUserArgs(t,
		question.Question{Header: "Storage", Question: "Which store?", Options: []question.Option{{Label: "Postgres"}, {Label: "SQLite"}}},
	), Context{SessionID: "ses_42"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if gotSession != "ses_42" {
		t.Errorf("Ask received session %q, want ses_42", gotSession)
	}
	if len(gotQuestions) != 1 || gotQuestions[0].Header != "Storage" {
		t.Errorf("Ask received %+v, want the batch as given", gotQuestions)
	}
	if res.Title != "Storage" {
		t.Errorf("Title = %q, want the first question's header", res.Title)
	}
	for _, want := range []string{"Which store?", "Selected: Postgres", "and a pooler"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("tool result is missing %q:\n%s", want, res.Output)
		}
	}
}

// An empty answer is a legitimate reply — the user looked and had no answer.
// The result must say so plainly, so the model can tell it apart from a question
// whose answer went missing, without claiming the blank was permission to
// invent one.
func TestAskUserTool_EmptyAnswerIsReportedAsABlank(t *testing.T) {
	tool := AskUserTool{Ask: func(context.Context, string, []question.Question) (question.Reply, error) {
		return question.Reply{Answers: []question.Answer{{}}}, nil
	}}
	res, err := tool.Execute(context.Background(), askUserArgs(t, question.Question{Question: "Which store?"}), Context{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(res.Output, "no answer") {
		t.Errorf("an empty answer should be reported as a deliberate blank, got:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "proceed on your own judgement") {
		t.Errorf("a blank answer is a non-answer, not a licence to invent one, got:\n%s", res.Output)
	}
}

// The description is read by the model before it decides to call anything, so
// the two halves of the rule must both be present: the tool is the channel for
// whatever only the user can answer (including a back-and-forth they are meant
// to play along with), and a question the agent could answer itself is not its
// business.
func TestAskUserTool_DescriptionNamesTheChannelAndItsLimit(t *testing.T) {
	desc := AskUserTool{}.Description()
	for _, want := range []string{"quiz", "survey", "walk-through", "source code"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description does not mention %q — the channel it now is goes unstated", want)
		}
	}
	if !strings.Contains(desc, "find out yourself") {
		t.Error("description does not state that what the agent can look up itself must not be asked")
	}
}

// A batch reply carries one answer per question, in order. The result must echo
// each question with its own answer so the model never has to re-pair them.
func TestAskUserTool_BatchAnswersArePairedInOrder(t *testing.T) {
	tool := AskUserTool{Ask: func(context.Context, string, []question.Question) (question.Reply, error) {
		return question.Reply{Answers: []question.Answer{
			{Selected: []string{"Postgres"}},
			{Text: "blue-green"},
		}}, nil
	}}
	res, err := tool.Execute(context.Background(), askUserArgs(t,
		question.Question{Header: "Storage", Question: "Which store?"},
		question.Question{Header: "Rollout", Question: "How to deploy?"},
	), Context{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	storage := strings.Index(res.Output, "Which store?")
	rollout := strings.Index(res.Output, "How to deploy?")
	if storage < 0 || rollout < 0 || storage > rollout {
		t.Fatalf("questions are not echoed in order:\n%s", res.Output)
	}
	if !strings.Contains(res.Output[storage:rollout], "Postgres") {
		t.Error("the first answer is not paired with the first question")
	}
	if !strings.Contains(res.Output[rollout:], "blue-green") {
		t.Error("the second answer is not paired with the second question")
	}
}

func TestAskUserTool_EmptyBatchIsHelpfulNotFatal(t *testing.T) {
	called := false
	tool := AskUserTool{Ask: func(context.Context, string, []question.Question) (question.Reply, error) {
		called = true
		return question.Reply{}, nil
	}}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[]}`), Context{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if called {
		t.Error("Ask was called with an empty batch")
	}
	if res.Output == "" {
		t.Error("an empty batch should explain what is required")
	}
}

func TestAskUserTool_TooManyQuestionsRefused(t *testing.T) {
	called := false
	tool := AskUserTool{Ask: func(context.Context, string, []question.Question) (question.Reply, error) {
		called = true
		return question.Reply{}, nil
	}}
	qs := make([]question.Question, maxAskUserQuestions+1)
	for i := range qs {
		qs[i] = question.Question{Question: "q"}
	}
	res, err := tool.Execute(context.Background(), askUserArgs(t, qs...), Context{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if called {
		t.Error("Ask was called with more questions than the dialog will show one batch of")
	}
	if !strings.Contains(res.Output, "at most") {
		t.Errorf("the refusal should name the limit, got: %q", res.Output)
	}
}

func TestAskUserTool_UnavailableIsAResultNotAnError(t *testing.T) {
	// A tool with no Ask wired (a headless registry) must answer the call rather
	// than error, so the model can carry on with its own judgement.
	res, err := AskUserTool{}.Execute(context.Background(), askUserArgs(t, question.Question{Question: "Which store?"}), Context{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(res.Output, "not available") {
		t.Errorf("expected an unavailability message, got: %q", res.Output)
	}
}

// A cancelled run unwinds through the error, the way the permission deny path
// does — it is not a tool result the model should react to and retry.
func TestAskUserTool_CancellationReturnsError(t *testing.T) {
	tool := AskUserTool{Ask: func(context.Context, string, []question.Question) (question.Reply, error) {
		return question.Reply{}, context.Canceled
	}}
	_, err := tool.Execute(context.Background(), askUserArgs(t, question.Question{Question: "Which store?"}), Context{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation to propagate", err)
	}
}

func TestAskUserTool_MalformedArgsError(t *testing.T) {
	if _, err := (AskUserTool{}).Execute(context.Background(), json.RawMessage(`{"questions":`), Context{}); err == nil {
		t.Error("malformed args should be an error")
	}
}

// The schema is what the model reads to build a call, so the parts the dialog
// relies on must be declared: the batch bounds, per-question fields, and the
// option shape.
func TestAskUserTool_SchemaDeclaresTheBatchShape(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(AskUserTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	qs, _ := props["questions"].(map[string]any)
	if qs == nil {
		t.Fatal("schema does not declare a questions array")
	}
	if min, _ := qs["minItems"].(float64); min != 1 {
		t.Errorf("questions minItems = %v, want 1", qs["minItems"])
	}
	if max, _ := qs["maxItems"].(float64); max != maxAskUserQuestions {
		t.Errorf("questions maxItems = %v, want %d", qs["maxItems"], maxAskUserQuestions)
	}
	item, _ := qs["items"].(map[string]any)
	itemProps, _ := item["properties"].(map[string]any)
	for _, want := range []string{"header", "question", "multiSelect", "options"} {
		if itemProps[want] == nil {
			t.Errorf("question schema is missing %q", want)
		}
	}
	if req, _ := item["required"].([]any); len(req) != 1 || req[0] != "question" {
		t.Errorf("question required = %v, want [question]", item["required"])
	}
}
