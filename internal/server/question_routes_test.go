package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/question"
)

func newQuestionTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	srv := &Server{bus: bus.New(16), questions: question.NewManager()}
	r := chi.NewRouter()
	r.Get("/session/{sessionID}/question", srv.handleListQuestions)
	r.Post("/session/{sessionID}/question/{questionID}", srv.handleReplyQuestion)
	return srv, r
}

func TestServe_ListQuestions(t *testing.T) {
	srv, r := newQuestionTestServer(t)
	srv.questions.Create(question.Request{
		ID: question.NewQuestionID(), SessionID: "ses_1",
		Questions: []question.Question{{Header: "Storage", Question: "Which store?"}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/ses_1/question", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var got []question.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got) != 1 || got[0].Questions[0].Header != "Storage" {
		t.Fatalf("list returned %+v, want the pending batch", got)
	}

	// Another session sees nothing — the dialog is per-session.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/ses_2/question", nil))
	if body := strings.TrimSpace(rec.Body.String()); body != "null" && body != "[]" {
		t.Errorf("a different session got %q, want no pending questions", body)
	}
}

func TestServe_ReplyQuestion(t *testing.T) {
	srv, r := newQuestionTestServer(t)
	req := question.Request{ID: question.NewQuestionID(), SessionID: "ses_1"}
	pr := srv.questions.Create(req)

	body := strings.NewReader(`{"answers":[{"selected":["Postgres"],"text":"with a pooler"}]}`)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/session/ses_1/question/%s", req.ID), body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reply status = %d, want 204", rec.Code)
	}

	select {
	case got := <-pr.ReplyCh:
		if len(got.Answers) != 1 || got.Answers[0].Text != "with a pooler" {
			t.Errorf("waiter got %+v, want the posted answers", got)
		}
	default:
		t.Fatal("the reply never reached the waiting loop")
	}

	// The batch is gone, so a retry is a 404 the client can act on.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/session/ses_1/question/%s", req.ID), strings.NewReader(`{"answers":[]}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("second reply status = %d, want 404", rec.Code)
	}
}

func TestServe_ReplyQuestionUnknown(t *testing.T) {
	_, r := newQuestionTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/session/ses_1/question/qst_missing",
		strings.NewReader(`{"answers":[]}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown question", rec.Code)
	}
}

func TestServe_ReplyQuestionBadBody(t *testing.T) {
	_, r := newQuestionTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/session/ses_1/question/qst_x",
		strings.NewReader(`{"answers":`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rec.Code)
	}
}

func listQuestions(t *testing.T, h http.Handler, sessionID string) []question.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/"+sessionID+"/question", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET question = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got []question.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode questions: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// A teammate's pending batch must appear in the queue of the session the human
// is watching, at any nesting depth, labelled with the teammate that asked —
// the same surfacing its permission prompts get. The request's SessionID keeps
// naming the blocked teammate: that is the loop the answers must unblock.
func TestParentSessionListsTeammateQuestions(t *testing.T) {
	srv, h := permissionTreeServer(t)
	srv.questions = question.NewManager()
	srv.questions.Create(question.Request{
		ID: "child-qst", SessionID: "sess-child",
		Questions: []question.Question{{Header: "Storage", Question: "Which store?"}},
	})
	srv.questions.Create(question.Request{
		ID: "grand-qst", SessionID: "sess-grand",
		Questions: []question.Question{{Header: "Layout", Question: "Where?"}},
	})

	byID := map[question.QuestionID]question.Request{}
	for _, req := range listQuestions(t, h, "sess-parent") {
		byID[req.ID] = req
	}
	if len(byID) != 2 {
		t.Fatalf("parent queue = %+v, want both teammates' batches", byID)
	}
	if got := byID["child-qst"].Teammate; got != "t1" {
		t.Fatalf("child batch teammate = %q, want t1", got)
	}
	if got := byID["child-qst"].SessionID; got != "sess-child" {
		t.Fatalf("child batch session = %q, want sess-child (the blocked loop)", got)
	}
	if got := byID["grand-qst"].Teammate; got != "t2" {
		t.Fatalf("grandchild batch teammate = %q, want t2", got)
	}

	// A leaf teammate and a session that never joined the board see only their
	// own (none) — the feature must not widen an ordinary session's queue.
	if got := listQuestions(t, h, "sess-grand"); len(got) != 1 || got[0].ID != "grand-qst" {
		t.Fatalf("leaf teammate = %+v, want only its own batch", got)
	}
	if got := listQuestions(t, h, "nobody"); len(got) != 0 {
		t.Fatalf("unknown session = %+v, want none", got)
	}
	// t2 hangs off t1, so the child's queue shows its own batch plus its child's
	// — the same nesting depth the parent's queue shows.
	if got := listQuestions(t, h, "sess-child"); len(got) != 2 {
		t.Fatalf("child queue = %+v, want its own batch and its child's", got)
	}
}

// Answering a teammate's ask_user from the parent's composer must reach the
// teammate's blocked loop and clear both queues: the one that showed the dialog
// and the one the teammate owns.
func TestReplyingAQuestionFromTheParentUnblocksTheTeammate(t *testing.T) {
	srv, h := permissionTreeServer(t)
	srv.questions = question.NewManager()
	events := srv.bus.SubscribeAll()
	pr := srv.questions.Create(question.Request{
		ID: "child-qst", SessionID: "sess-child",
		Questions: []question.Question{{Header: "Storage", Question: "Which store?"}},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/session/sess-parent/question/child-qst",
		strings.NewReader(`{"answers":[{"selected":["Postgres"]}]}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST reply = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	select {
	case got := <-pr.ReplyCh:
		if len(got.Answers) != 1 || got.Answers[0].Selected[0] != "Postgres" {
			t.Fatalf("teammate's loop got %+v, want the posted answers", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("teammate's loop was never unblocked")
	}

	told := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(told) < 2 {
		select {
		case ev := <-events:
			if ev.Type != "question.replied" {
				continue
			}
			var p map[string]string
			if err := json.Unmarshal(ev.Properties, &p); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if p["questionId"] != "child-qst" {
				t.Fatalf("event names %q, want child-qst", p["questionId"])
			}
			told[p["sessionId"]] = true
		case <-deadline:
			t.Fatalf("question.replied reached %v, want both sess-parent and sess-child", told)
		}
	}
	if !told["sess-parent"] || !told["sess-child"] {
		t.Fatalf("question.replied reached %v, want both sess-parent and sess-child", told)
	}

	// The batch is gone: the child's own queue is empty, and a second answer is
	// a 404 the client can act on.
	if got := listQuestions(t, h, "sess-child"); len(got) != 0 {
		t.Fatalf("child queue after the reply = %+v, want empty", got)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/session/sess-parent/question/child-qst", strings.NewReader(`{"answers":[]}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second reply = %d, want 404", rec.Code)
	}
}

// A server built without the manager (a test, or any path that skips it) must
// answer rather than panic.
func TestServe_QuestionsWithoutManager(t *testing.T) {
	srv := &Server{bus: bus.New(4)}
	rec := httptest.NewRecorder()
	srv.handleListQuestions(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("list status = %d, want 200 with an empty list", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleReplyQuestion(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"answers":[]}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("reply status = %d, want 404", rec.Code)
	}
}
