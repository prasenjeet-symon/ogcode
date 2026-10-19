package provider

import (
	"io"
	"sync"
	"testing"
	"time"
)

// closingBody is a response body whose Close blocks until the test releases it,
// which makes the teardown order of a stream observable from the outside: while
// Close is in progress, the event channel is either still open or already closed.
type closingBody struct {
	closing chan struct{}
	release chan struct{}
	once    sync.Once
}

func newClosingBody() *closingBody {
	return &closingBody{closing: make(chan struct{}), release: make(chan struct{})}
}

func (b *closingBody) Read(p []byte) (int, error) { return 0, io.EOF }

func (b *closingBody) Close() error {
	b.once.Do(func() { close(b.closing) })
	<-b.release
	return nil
}

// TestStreamEventsClosesBodyBeforeChannel pins the teardown order of a provider
// stream: the response body is closed — releasing its in-flight budget slot —
// BEFORE the event channel is closed. The consumer wakes on the channel close and
// may start the next completion at once, so closing the channel first lets a
// second request overlap a body that has not been released yet. A gateway that
// caps completions in flight answers that overlap with a 429 (OGX: "too many
// completions in flight"), which is the failure this ordering prevents.
func TestStreamEventsClosesBodyBeforeChannel(t *testing.T) {
	cases := []struct {
		provider string
		run      func(body io.ReadCloser, ch chan<- StreamEvent)
	}{
		{
			provider: "openai",
			run: func(body io.ReadCloser, ch chan<- StreamEvent) {
				p := &OpenAIProvider{}
				p.streamEvents(body, ch, func() {}, nil)
			},
		},
		{
			provider: "anthropic",
			run: func(body io.ReadCloser, ch chan<- StreamEvent) {
				p := &AnthropicProvider{}
				p.streamEvents(body, ch, func() {})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			body := newClosingBody()
			ch := make(chan StreamEvent, 256)
			done := make(chan struct{})
			go func() {
				defer close(done)
				tc.run(body, ch)
			}()

			// EOF is immediate, so reaching Close proves a clean end of stream
			// closes the body rather than leaking it.
			select {
			case <-body.closing:
			case <-time.After(5 * time.Second):
				t.Fatal("stream never closed its response body")
			}

			select {
			case _, ok := <-ch:
				if !ok {
					close(body.release)
					<-done
					t.Fatal("event channel closed while the response body was still open")
				}
			default:
			}

			close(body.release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not finish after the body was released")
			}
			for range ch {
			}
		})
	}
}
