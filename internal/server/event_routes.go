package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if present

	// Tell the browser to reconnect after 200 ms if the connection drops.
	// This replaces the client-side exponential backoff and keeps the session
	// always listening without long gaps.
	fmt.Fprintf(w, "retry: 200\n\n")
	flusher.Flush()

	connected, _ := json.Marshal(map[string]any{"type": "server.connected"})
	fmt.Fprintf(w, "data: %s\n\n", connected)
	flusher.Flush()

	// Send initial config so clients know memory status etc. without polling.
	cfgPayload := s.configPayload()
	configData, _ := json.Marshal(map[string]any{
		"type":       "server.config",
		"properties": cfgPayload,
	})
	fmt.Fprintf(w, "data: %s\n\n", configData)
	flusher.Flush()

	ch := s.bus.SubscribeAll()
	defer s.bus.Unsubscribe(ch)

	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	// Live resource samples ride the stream as control frames, alongside
	// connected/config/heartbeat, rather than going through the bus. They are
	// periodic telemetry, not session state: publishing them would burn a
	// sequence number every couple of seconds and turn a dropped sample into a
	// seq gap that makes the client resync everything it has.
	//
	// Registering as a watcher is also what starts the sampler — with no UI
	// open, nothing is measured.
	var resourceTick <-chan time.Time
	if s.resources != nil {
		s.resources.AddWatcher()
		defer s.resources.RemoveWatcher()
		sampleTicker := time.NewTicker(s.resources.Interval())
		defer sampleTicker.Stop()
		resourceTick = sampleTicker.C
	}

	done := r.Context().Done()

	for {
		select {
		case <-done:
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				slog.Error("marshal event", "err", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-resourceTick:
			// A nil channel blocks forever, so this arm is simply never taken
			// when there is no sampler.
			sample, ok := s.resources.Latest()
			if !ok {
				continue
			}
			meta := s.resources.Meta()
			frame, err := json.Marshal(map[string]any{
				"type": "server.resources",
				"properties": map[string]any{
					"interval": meta.Interval,
					"cores":    meta.Cores,
					"uptime":   meta.Uptime,
					"activity": meta.Activity,
					"sample":   sample,
				},
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", frame)
			flusher.Flush()
		case <-heartbeat.C:
			hb, _ := json.Marshal(map[string]any{"type": "server.heartbeat"})
			fmt.Fprintf(w, "data: %s\n\n", hb)
			flusher.Flush()
		}
	}
}