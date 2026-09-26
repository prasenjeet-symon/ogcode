// Package question carries the ask_user round trip: the agent loop registers a
// batch of questions it cannot answer on its own, blocks until a client replies,
// and resumes with the answers as the tool result.
//
// It is the sibling of internal/permission, and deliberately the same shape — a
// request that is created, announced on the bus, and awaited on a buffered reply
// channel, so the HTTP handler that answers it runs on a different goroutine
// from the one blocked on it. The difference is the payload: a permission is
// answered with a fixed word, a question is answered with free-form structure
// (selected option labels plus typed text) that only this package knows how to
// carry.
package question

import (
	"strings"
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/id"
)

type QuestionID = id.QuestionID

func NewQuestionID() QuestionID { return id.NewQuestionID() }

// Option is one proposed answer the model offered. It is a suggestion, not a
// constraint: the dialog always carries a free-text field, so the set need not
// be exhaustive and the user is free to answer in their own words.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Condition gates a question on an answer already given — the branching that
// lets one screen decide what the next screen asks. It names an earlier
// question by id and the option labels that reveal this one.
type Condition struct {
	// Question is the id of an earlier question in the same batch.
	Question string `json:"question"`
	// Options are option labels of that question. The gated screen is shown
	// when the answer selected any of them; an empty list matches any answer at
	// all (a selection or typed text).
	Options []string `json:"options,omitempty"`
	// Not inverts the match: shown when none of Options was selected.
	Not bool `json:"not,omitempty"`
}

// Question is one prompt within a batch. Header is the short title shown on the
// question's own screen; MultiSelect says whether several options may be
// selected at once (single-select is the default).
//
// ID is a short slug another question can branch on; ShowWhen makes this
// screen conditional on an earlier answer. A question with no ShowWhen is
// always shown, so a batch that does not branch is unchanged.
type Question struct {
	ID          string     `json:"id,omitempty"`
	Header      string     `json:"header,omitempty"`
	Question    string     `json:"question"`
	Options     []Option   `json:"options,omitempty"`
	MultiSelect bool       `json:"multiSelect,omitempty"`
	ShowWhen    *Condition `json:"showWhen,omitempty"`
}

// Match reports whether the condition holds for answers keyed by question id. A
// nil condition (a screen with no ShowWhen) always matches.
func (c *Condition) Match(answers map[string]Answer) bool {
	if c == nil {
		return true
	}
	a := answers[c.Question]
	hit := false
	if len(c.Options) == 0 {
		hit = len(a.Selected) > 0 || strings.TrimSpace(a.Text) != ""
	} else {
		for _, sel := range a.Selected {
			for _, want := range c.Options {
				if sel == want {
					hit = true
				}
			}
		}
	}
	if c.Not {
		return !hit
	}
	return hit
}

// AnswersByID pairs a positional reply with the batch, keyed by question id. A
// question without an id is left out: nothing can branch on it.
func AnswersByID(questions []Question, reply Reply) map[string]Answer {
	m := make(map[string]Answer, len(questions))
	for i, q := range questions {
		if q.ID == "" {
			continue
		}
		if i < len(reply.Answers) {
			m[q.ID] = reply.Answers[i]
		}
	}
	return m
}

// Visibility reports, for each question in order, whether the user is shown it
// given the answers to the questions before it. Because a condition only ever
// names an earlier question, the walk is a single pass — a screen behind a
// skipped branch is skipped too, since its condition then reads a blank answer.
func Visibility(questions []Question, answers map[string]Answer) []bool {
	vis := make([]bool, len(questions))
	for i, q := range questions {
		vis[i] = q.ShowWhen.Match(answers)
	}
	return vis
}

// Request is a whole batch, as handed to the UI.
type Request struct {
	ID        QuestionID `json:"questionId"`
	SessionID string     `json:"sessionId"`
	Questions []Question `json:"questions"`
}

// Answer is the reply to one question. Selected carries the labels chosen from
// the model's options; Text carries whatever the user typed. Either may be
// empty: a blank answer is a legitimate non-answer, and what the model should do
// with it depends on the question — a preference left open is licence to use its
// own judgement, whereas a question whose content it actually needs was simply
// not answered.
type Answer struct {
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// Reply is the reply to a batch: one Answer per question, in the order the
// questions were asked.
type Reply struct {
	Answers []Answer `json:"answers"`
}

// PendingRequest holds a batch awaiting a reply.
type PendingRequest struct {
	Request Request
	ReplyCh chan Reply
}

// Manager holds the pending batches. It is safe for concurrent use: the loop
// goroutine calls Create/Remove while the HTTP handler goroutine calls Reply.
type Manager struct {
	mu      sync.Mutex
	pending map[QuestionID]*PendingRequest
}

func NewManager() *Manager {
	return &Manager{pending: make(map[QuestionID]*PendingRequest)}
}

func (m *Manager) Create(req Request) *PendingRequest {
	pr := &PendingRequest{
		Request: req,
		ReplyCh: make(chan Reply, 1),
	}
	m.mu.Lock()
	m.pending[req.ID] = pr
	m.mu.Unlock()
	return pr
}

func (m *Manager) Get(id QuestionID) *PendingRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[id]
}

// Remove discards a pending batch without replying — used when the request is
// abandoned (e.g. the agent loop was cancelled while waiting for an answer).
func (m *Manager) Remove(id QuestionID) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

// PendingForSession returns all unanswered batches for a session. The UI uses
// this to restore the dialog when the user switches back to a session whose
// question was hidden by the view change — the agent loop is still blocked.
// Order is not guaranteed (map iteration).
func (m *Manager) PendingForSession(sessionID string) []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Request
	for _, pr := range m.pending {
		if pr.Request.SessionID == sessionID {
			out = append(out, pr.Request)
		}
	}
	return out
}

// Reply answers a pending batch and reports whether it was still pending. The
// first client to answer wins: the entry is removed before the send, so a
// second caller finds nothing and gets false.
func (m *Manager) Reply(id QuestionID, reply Reply) bool {
	m.mu.Lock()
	pr := m.pending[id]
	if pr != nil {
		delete(m.pending, id)
	}
	m.mu.Unlock()
	if pr == nil {
		return false
	}
	// ReplyCh is buffered (cap 1), so this never blocks even if the waiter has
	// already given up (e.g. on ctx cancellation).
	pr.ReplyCh <- reply
	return true
}
