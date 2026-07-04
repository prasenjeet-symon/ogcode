package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// Hardcoded PostHog project credentials. These are baked into the binary —
// users have no control over analytics configuration.
const (
	PostHogAPIKey  = "phc_CGzEmfPURHyNWrG49yNJA7wY5io8URFu3sazRYTAXw6Z"
	PostHogAPIHost = "https://app.posthog.com"
)

// PostHogClient captures server-side analytics events to a PostHog project.
// Events are sent via the /capture REST endpoint — no SDK dependency required.
// All calls are fire-and-forget with a bounded worker goroutine so they never
// block request handlers.
type PostHogClient struct {
	apiKey string
	host   string
	http   *http.Client

	queue   chan posthogEvent
	stopped chan struct{}
	wg      sync.WaitGroup
}

type posthogEvent struct {
	Event      string            `json:"event"`
	DistinctID string            `json:"distinct_id"`
	Properties map[string]any    `json:"properties"`
}

type posthogCapture struct {
	APIKey    string      `json:"api_key"`
	Event     string      `json:"event"`
	DistinctID string     `json:"distinct_id"`
	Properties map[string]any `json:"properties,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// NewPostHogClient returns an active client that starts a background worker.
// Returns nil if apiKey is empty.
func NewPostHogClient(apiKey, host string) *PostHogClient {
	if apiKey == "" {
		return nil
	}
	if host == "" {
		host = PostHogAPIHost
	}
	c := &PostHogClient{
		apiKey: apiKey,
		host:   host,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		queue:   make(chan posthogEvent, 256),
		stopped: make(chan struct{}),
	}
	c.wg.Add(1)
	go c.worker()
	return c
}

// Capture enqueues an event. Properties are merged with the default server-side
// properties (os, arch, version). The call is non-blocking — if the queue is
// full the event is dropped silently.
func (c *PostHogClient) Capture(event, distinctID string, props map[string]any) {
	if c == nil {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	props["os"] = runtime.GOOS
	props["arch"] = runtime.GOARCH
	props["version"] = version.Version
	if hostname, err := os.Hostname(); err == nil {
		props["hostname"] = hostname
	}
	select {
	case c.queue <- posthogEvent{Event: event, DistinctID: distinctID, Properties: props}:
	default:
		// queue full — drop event
	}
}

// Stop drains the queue and shuts down the worker.
func (c *PostHogClient) Stop() {
	if c == nil {
		return
	}
	close(c.stopped)
	c.wg.Wait()
}

func (c *PostHogClient) worker() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopped:
			// drain remaining events
			for {
				select {
				case ev := <-c.queue:
					c.send(ev)
				default:
					return
				}
			}
		case ev := <-c.queue:
			c.send(ev)
		}
	}
}

func (c *PostHogClient) send(ev posthogEvent) {
	payload := posthogCapture{
		APIKey:     c.apiKey,
		Event:      ev.Event,
		DistinctID: ev.DistinctID,
		Properties: ev.Properties,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	url := c.host + "/capture"
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// posthogDistinctID returns a stable anonymous identifier for this machine.
// It uses the hostname; if unavailable, falls back to "ogcode-server".
func posthogDistinctID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return fmt.Sprintf("server-%s", h)
	}
	return "ogcode-server"
}