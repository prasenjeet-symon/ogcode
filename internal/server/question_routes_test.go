package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
