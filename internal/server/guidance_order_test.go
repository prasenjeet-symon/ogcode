package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// withLoop registers a running loop's control for sid, the way starting a
// turn does. newTestServer builds a bare Server, so the map, the bus and the
// transcript store the handler writes the guidance into may not exist yet.
func withLoop(s *Server, sid session.SessionID) *agent.LoopControl {
	if s.bus == nil {
		s.bus = bus.New(64)
	}
	if s.store == nil {
		s.store = session.NewStore(s.db)
	}
	lc := agent.NewLoopControl()
	s.mu.Lock()
	if s.loopControls == nil {
		s.loopControls = map[session.SessionID]*agent.LoopControl{}
	}
	s.loopControls[sid] = lc
	s.mu.Unlock()
	return lc
}

// TestGuidanceIsQueuedBeforeItCanBeDelivered pins the order of the two
// loop.guidance events. The loop publishes "delivered" as soon as it drains
// the queue, which it can do the instant the guidance is pushed; if "queued"
// were published after that, the client would see it last and keep showing
// "Guidance queued" over guidance the loop had already applied — for the rest
// of the turn. A goroutine standing in for the loop drains as fast as it can,
// so the old order (push, cancel, then publish) loses this race.
func TestGuidanceIsQueuedBeforeItCanBeDelivered(t *testing.T) {
	s := newTestServer(t)
	sid := session.SessionID("ses_guidance_order")
	lc := withLoop(s, sid)
	events := s.bus.SubscribeAll()

	// The loop: drain the instant anything is pushed, then say so.
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if lc.DrainGuidance() != "" {
				s.bus.Publish("loop.guidance", map[string]string{"sessionId": string(sid), "status": "delivered"})
				return
			}
			runtime.Gosched()
		}
	}()

	body := strings.NewReader(`{"content":"use the staging box instead","cancelTool":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/"+string(sid)+"/guidance", body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionID", string(sid))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleGuidance(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("guidance: status %d, body %s", rec.Code, rec.Body.String())
	}
	<-loopDone

	var order []string
	timeout := time.After(2 * time.Second)
	for len(order) < 2 {
		select {
		case evt := <-events:
			if evt.Type != "loop.guidance" {
				continue
			}
			var props map[string]string
			if err := json.Unmarshal(evt.Properties, &props); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			order = append(order, props["status"])
		case <-timeout:
			t.Fatalf("saw only %v, want queued then delivered", order)
		}
	}
	if order[0] != "queued" || order[1] != "delivered" {
		t.Fatalf("loop.guidance order = %v, want [queued delivered]", order)
	}
}

// A cancel-only request queues no guidance, so it must not announce any:
// nothing would ever be delivered to clear the client's indicator.
func TestCancelOnlyGuidanceAnnouncesNothing(t *testing.T) {
	s := newTestServer(t)
	sid := session.SessionID("ses_cancel_only")
	withLoop(s, sid)
	events := s.bus.SubscribeAll()

	req := httptest.NewRequest(http.MethodPost, "/api/session/"+string(sid)+"/guidance",
		strings.NewReader(`{"content":"","cancelTool":true}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionID", string(sid))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleGuidance(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel-only: status %d", rec.Code)
	}
	for {
		select {
		case evt := <-events:
			if evt.Type == "loop.guidance" {
				t.Fatalf("a cancel-only request published %s", evt.Properties)
			}
		default:
			return
		}
	}
}
