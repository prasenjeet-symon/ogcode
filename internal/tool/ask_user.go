package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/question"
)

// AskUserFunc puts a batch of questions in front of the user and blocks until
// they answer. Implemented by agent.LoopRunner.AskUser and wired in from
// server.go/worker to avoid the tool→agent import cycle.
type AskUserFunc func(ctx context.Context, sessionID string, questions []question.Question) (question.Reply, error)

// maxAskUserQuestions caps a batch so the dialog stays a short set of screens
// rather than an interrogation. The guidance in the system prompt asks for 2-5.
const maxAskUserQuestions = 5

// AskUserTool puts a small batch of questions to the user and waits for the
// answers. It is the channel for whatever only the user can tell the agent — a
// preference, a constraint, a choice with real trade-offs, or a step of
// something they are meant to play along with (a quiz, a survey, a
// walk-through). What the agent can find out for itself — from the code, a
// file, the web, a command — does not belong here. The batch is rendered as
// paginated screens in one dialog; the answers come back as the tool result and
// the turn continues. It is a blocking round trip — the turn pauses until the
// user answers (or the run is cancelled).
//
// It is only offered to an interactive session: the tool is registered where a
// question manager exists and the loop only offers it under permission gating
// (see LoopRunner.RunLoop). Headless runs and sub-agents never see it.
type AskUserTool struct {
	Ask AskUserFunc
}

func (AskUserTool) ID() string { return "ask_user" }

func (AskUserTool) Description() string {
	return "Put a small batch of questions to the user and wait for their answers. " +
		"Whatever you would otherwise ask in prose, ask here instead — including a back-and-forth the user is meant to play along with " +
		"(a quiz, a survey, a walk-through): each question becomes a screen with buttons, so one click answers it. " +
		"Reserve it for what only a person can tell you — a preference, a constraint, a choice between approaches with real trade-offs. " +
		"Anything you can find out yourself (in the source code, in a file, on the web, by running a command) you look up instead; " +
		"do not ask it here, and do not use it to confirm an obvious next step. " +
		"Ask everything you need in ONE call: each question has a short header, the question text, and 2-4 concrete options you propose " +
		"(the user can always type their own answer instead), marked multi-select only when several options may apply at once. " +
		"The turn pauses until the user answers."
}

func (AskUserTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"questions": {
				"type": "array",
				"description": "The batch of questions, shown one per screen. Ask everything you need in one call (2-5 questions).",
				"minItems": 1,
				"maxItems": 5,
				"items": {
					"type": "object",
					"properties": {
						"header": {
							"type": "string",
							"description": "A short (2-5 word) title for the question, shown as the screen's heading, e.g. \"Storage choice\"."
						},
						"question": {
							"type": "string",
							"description": "The question itself, in one clear sentence."
						},
						"multiSelect": {
							"type": "boolean",
							"description": "Allow several options to be selected at once. Default false (pick one). Use it only when options are not mutually exclusive."
						},
						"options": {
							"type": "array",
							"description": "2-4 concrete answers you propose, in preference order. The user can always type an answer of their own, so this need not be exhaustive.",
							"minItems": 2,
							"maxItems": 6,
							"items": {
								"type": "object",
								"properties": {
									"label": {
										"type": "string",
										"description": "The option's short label, e.g. \"Postgres\"."
									},
									"description": {
										"type": "string",
										"description": "One line explaining the option or its trade-off."
									}
								},
								"required": ["label"]
							}
						}
					},
					"required": ["question"]
				}
			}
		},
		"required": ["questions"]
	}`)
}

func (t AskUserTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Questions []question.Question `json:"questions"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if len(input.Questions) == 0 {
		return Result{Output: "questions is required — put the batch you need answered in the questions array"}, nil
	}
	if len(input.Questions) > maxAskUserQuestions {
		return Result{Output: fmt.Sprintf("too many questions (%d) — ask at most %d in one call", len(input.Questions), maxAskUserQuestions)}, nil
	}
	if t.Ask == nil {
		return Result{Output: "ask_user is not available in this environment — proceed on your own judgement and say what you assumed"}, nil
	}

	title := "questions"
	if h := input.Questions[0].Header; h != "" {
		title = h
	}

	reply, err := t.Ask(ctx, string(tctx.SessionID), input.Questions)
	if err != nil {
		// Mirrors the permission deny path: the wait ended because the run was
		// cancelled, so the error unwinds the loop rather than being reported
		// to the model as a tool result it should react to.
		return Result{}, err
	}
	return Result{Title: title, Output: formatAnswers(input.Questions, reply)}, nil
}

// formatAnswers renders the answered batch back to the model. Every question is
// echoed with whatever the user gave it, so the model can tell an answer from a
// deliberate blank and does not have to hold the batch in its head.
func formatAnswers(questions []question.Question, reply question.Reply) string {
	var b strings.Builder
	b.WriteString("The user answered:\n")
	for i, q := range questions {
		label := q.Header
		if label == "" {
			label = q.Question
		}
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, label)
		if q.Header != "" {
			fmt.Fprintf(&b, "   Q: %s\n", q.Question)
		}

		var a question.Answer
		if i < len(reply.Answers) {
			a = reply.Answers[i]
		}
		if len(a.Selected) > 0 {
			fmt.Fprintf(&b, "   Selected: %s\n", strings.Join(a.Selected, ", "))
		}
		if a.Text != "" {
			fmt.Fprintf(&b, "   Answer: %s\n", a.Text)
		}
		if len(a.Selected) == 0 && a.Text == "" {
			b.WriteString("   (no answer)\n")
		}
	}
	return b.String()
}
