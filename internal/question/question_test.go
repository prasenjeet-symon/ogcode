package question

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManager_CreateGetRemove(t *testing.T) {
	m := NewManager()
	req := Request{ID: NewQuestionID(), SessionID: "ses_1", Questions: []Question{{Question: "Which store?"}}}
	m.Create(req)

	if got := m.Get(req.ID); got == nil || got.Request.SessionID != "ses_1" {
		t.Fatalf("Get returned %+v, want the created request", got)
	}
	m.Remove(req.ID)
	if m.Get(req.ID) != nil {
		t.Error("Remove left the request pending")
	}
}

// Reply hands the answers to the blocked waiter exactly once. The second
// caller — a second browser tab, or a retry after the first landed — must be
// told the batch is gone rather than overwriting the answer the loop already
// resumed with.
func TestManager_ReplyFirstAnswerWins(t *testing.T) {
	m := NewManager()
	req := Request{ID: NewQuestionID(), SessionID: "ses_1"}
	pr := m.Create(req)

	first := Reply{Answers: []Answer{{Selected: []string{"Postgres"}}}}
	if !m.Reply(req.ID, first) {
		t.Fatal("Reply returned false for a pending request")
	}
	select {
	case got := <-pr.ReplyCh:
		if len(got.Answers) != 1 || got.Answers[0].Selected[0] != "Postgres" {
			t.Errorf("waiter got %+v, want the first reply", got)
		}
	default:
		t.Fatal("Reply did not deliver the answer")
	}

	if m.Reply(req.ID, Reply{Answers: []Answer{{Text: "second"}}}) {
		t.Error("a second Reply succeeded; only the first answer may win")
	}
}

func TestManager_ReplyUnknown(t *testing.T) {
	m := NewManager()
	if m.Reply(NewQuestionID(), Reply{}) {
		t.Error("Reply returned true for a request that was never created")
	}
}

// ReplyCh is buffered so a reply arriving after the waiter gave up (ctx
// cancelled) cannot block the handler goroutine. This is the same guarantee the
// permission manager relies on.
func TestManager_ReplyAfterWaiterGoneDoesNotBlock(t *testing.T) {
	m := NewManager()
	req := Request{ID: NewQuestionID()}
	m.Create(req)

	done := make(chan bool, 1)
	go func() { done <- m.Reply(req.ID, Reply{Answers: []Answer{{Text: "late"}}}) }()
	select {
	case ok := <-done:
		if !ok {
			t.Error("Reply should still report success while the entry was pending")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reply blocked when nobody was reading the channel")
	}
}

func TestManager_PendingForSession(t *testing.T) {
	m := NewManager()
	a := m.Create(Request{ID: NewQuestionID(), SessionID: "ses_a"})
	m.Create(Request{ID: NewQuestionID(), SessionID: "ses_b"})
	m.Create(Request{ID: NewQuestionID(), SessionID: "ses_a"})

	if got := m.PendingForSession("ses_a"); len(got) != 2 {
		t.Fatalf("PendingForSession(ses_a) returned %d requests, want 2", len(got))
	}
	if got := m.PendingForSession("ses_b"); len(got) != 1 {
		t.Errorf("PendingForSession(ses_b) returned %d requests, want 1", len(got))
	}

	// Answering one removes it from the pending set.
	m.Reply(a.Request.ID, Reply{})
	if got := m.PendingForSession("ses_a"); len(got) != 1 {
		t.Errorf("an answered request is still reported as pending (%d left)", len(got))
	}
}

// The wire shape is what the browser reads, so pin it: the ids and field names
// the dialog depends on must not drift silently.
func TestRequestJSONShape(t *testing.T) {
	req := Request{
		ID:        NewQuestionID(),
		SessionID: "ses_1",
		Questions: []Question{{
			Header:   "Storage",
			Question: "Which store?",
			Options:  []Option{{Label: "Postgres", Description: "managed"}},
		}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"questionId"`, `"sessionId"`, `"questions"`, `"header"`, `"question"`, `"options"`, `"label"`, `"description"`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshalled request is missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, `"multiSelect"`) {
		t.Error("multiSelect should be omitted when false — the dialog uses absence as single-select")
	}
	if !strings.HasPrefix(string(req.ID), "qst_") {
		t.Errorf("question id %q does not carry the qst_ prefix", req.ID)
	}
}

func TestManager_ConcurrentCreateReply(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := Request{ID: NewQuestionID(), SessionID: "ses_1"}
			m.Create(req)
			m.Reply(req.ID, Reply{})
		}()
	}
	wg.Wait()
	if got := m.PendingForSession("ses_1"); len(got) != 0 {
		t.Errorf("%d requests still pending after every one was answered", len(got))
	}
}
