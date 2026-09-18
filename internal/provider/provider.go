package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// streamIdleTimeout bounds how long a streaming response may go with NO data
// before it is aborted. Streaming requests carry no whole-request deadline (see
// streamHTTPClient), so this idle watchdog — reset on every chunk read off the
// wire — is what surfaces a dead connection. It is deliberately generous so a
// slow time-to-first-token doesn't trip it.
const streamIdleTimeout = 120 * time.Second

// streamIdleTimeoutBuffered is the idle budget for endpoints that do NOT stream
// tool-call arguments. Anthropic and OpenAI emit a tool call as a run of small
// deltas, so a working stream is never quiet for long and a tight budget is
// safe. Ollama composes the whole call and emits it in ONE frame when the model
// finishes — measured against a local endpoint: 13.4 seconds of complete
// silence for a 7 KB call, i.e. the wire stays silent for as long as the model
// spends writing the file. Under the tight budget that aborts healthy work on
// exactly the long-file turns that need it most, so these endpoints get a
// budget sized to outlast a large generation rather than a network blip.
const streamIdleTimeoutBuffered = 10 * time.Minute

// idleTimeoutEnv names the environment variable that overrides the built-in
// idle budgets above, for every endpoint. Those budgets are sized for what a
// provider's own wire behaviour makes normal, and that is not the same question
// as what a given machine makes normal: a local model on modest hardware can
// spend longer than streamIdleTimeoutBuffered in prompt evaluation alone — the
// connection silent throughout — and until this existed the only way to finish
// that turn was to rebuild with a larger constant.
const idleTimeoutEnv = "OGCODE_STREAM_IDLE_TIMEOUT"

// idleTimeoutNever switches the watchdog off. It is an enormous real duration
// rather than a flag so that every path carrying a budget keeps working
// unchanged: time.AfterFunc clamps an overflowing deadline to the far future,
// so the timer arms normally and simply never fires. Zero could not spell this,
// because newIdleWatchdog reads a non-positive budget as "use the default".
const idleTimeoutNever = time.Duration(math.MaxInt64)

// idleTimeoutFloor guards the bare-seconds spelling accepted below. "10" means
// ten seconds, but someone who means ten minutes will type exactly that, and a
// ten-second budget aborts even healthy streams. A value under the floor is far
// likelier to be that mistake than an intent, so it is refused and reported
// rather than honoured into a stream that can never finish.
const idleTimeoutFloor = 10 * time.Second

// idleTimeoutOverride is the operator's budget, read and parsed once. Zero means
// no usable override, leaving the built-in budgets in force.
var idleTimeoutOverride = sync.OnceValue(func() time.Duration {
	return parseIdleTimeout(os.Getenv(idleTimeoutEnv))
})

// parseIdleTimeout interprets the value of idleTimeoutEnv. It accepts a Go
// duration ("15m", "900s"), a bare count of seconds ("900"), or off/none/never
// to disable the watchdog. Anything it cannot use returns 0 — the built-in
// budget then applies, so a typo costs a warning rather than the whole stream.
func parseIdleTimeout(raw string) time.Duration {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return 0
	}
	switch v {
	case "0", "off", "none", "never":
		return idleTimeoutNever
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		// A bare number means seconds — the spelling people reach for first,
		// and the one a unit-less value in any other config would have.
		n, convErr := strconv.ParseInt(v, 10, 64)
		if convErr != nil {
			slog.Warn("ignoring unusable idle timeout, keeping the built-in budget",
				"env", idleTimeoutEnv, "value", raw)
			return 0
		}
		if n > int64(math.MaxInt64/time.Second) {
			// Too large to express as a Duration in nanoseconds. Multiplying
			// would silently wrap, so read the obvious intent instead.
			return idleTimeoutNever
		}
		d = time.Duration(n) * time.Second
	}
	if d == 0 {
		// "0s" and "00" reach here rather than the switch above, and mean what
		// the bare "0" there means.
		return idleTimeoutNever
	}
	if d < idleTimeoutFloor {
		slog.Warn("ignoring idle timeout below the floor, keeping the built-in budget",
			"env", idleTimeoutEnv, "value", raw, "floor", idleTimeoutFloor)
		return 0
	}
	return d
}

// resolveIdleTimeout picks the budget for one stream: the operator's override
// when they set a usable one, else the budget the endpoint earns on its own
// behaviour. Raising it trades away detection of a genuinely dead connection,
// which is why it is opt-in and never inferred.
func resolveIdleTimeout(builtin time.Duration) time.Duration {
	return pickIdleTimeout(idleTimeoutOverride(), builtin)
}

// pickIdleTimeout resolves one budget against an override. It is split out from
// resolveIdleTimeout so the choice can be tested without the process-wide env
// read, which sync.OnceValue pins for the life of the process.
func pickIdleTimeout(override, builtin time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	return builtin
}

// isLocalEndpoint reports whether a base URL points at this machine or the
// local network. Local model servers (Ollama, llama.cpp, LM Studio, vLLM and
// the relays people put in front of them) commonly batch tool calls the way
// Ollama does, and a loopback connection cannot suffer the network failures the
// tight idle budget exists to catch.
func isLocalEndpoint(baseURL string) bool {
	// isCloudURL already recognises loopback and the RFC1918 LAN ranges — reuse
	// it rather than restating that list in a second place.
	if !isCloudURL(baseURL) {
		return true
	}
	// It matches on substrings, so these host forms slip past it.
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	switch host := strings.ToLower(u.Hostname()); host {
	case "::1", "host.docker.internal":
		return true
	default:
		return strings.HasSuffix(host, ".local")
	}
}

// streamMaxLineBytes caps a single SSE line. Providers that do not chunk
// tool-call arguments send an entire call — a whole file's contents, for a write
// — in one `data:` line, and JSON escaping inflates it further. Too small a cap
// makes bufio fail with ErrTooLong part-way through a large response, which the
// agent loop can only report as a stream that ended without finishing.
const streamMaxLineBytes = 16 * 1024 * 1024

// streamResponseHeaderTimeout bounds the wait for response headers, the one
// phase the idle watchdog cannot cover (it only starts once the body exists).
// It is deliberately long: free and shared endpoints queue a request for
// minutes before answering, and that is a slow provider, not a dead connection.
const streamResponseHeaderTimeout = 300 * time.Second

// streamHTTPClient issues streaming requests. It deliberately has no
// Client.Timeout: that deadline covers the whole request including the body
// read, so a generation that legitimately runs long has its connection killed
// mid-stream. Liveness is bounded where it belongs instead — at connect, TLS and
// response-header time here, and by the per-stream idle watchdog once bytes are
// flowing.
var streamHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       dialStream,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      100,
		// Below the ~60s idle-close observed on local relay endpoints (measured:
		// connections survive 50s idle, are closed by 70s). Pooling a connection
		// for longer than the peer keeps it alive hands dead sockets to new
		// requests; the transport usually retries those, but not reliably enough
		// to be worth the race. The cost is a fresh handshake for requests spaced
		// more than 30s apart.
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: streamResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// forceIPv4Env names the environment variable that decides whether provider
// streams may use IPv6. It has three positions rather than two: unset leaves
// IPv6 in use with the automatic fallback below armed, a truthy value pins IPv4
// from the start, and an explicit off keeps IPv6 no matter how badly it behaves
// — the escape hatch for a host where IPv4 is the broken half.
const forceIPv4Env = "OGCODE_FORCE_IPV4"

type ipv4Mode int

const (
	// ipv4Auto is the default: IPv6 is used, and repeated IPv6-path failures
	// trip the automatic fallback.
	ipv4Auto ipv4Mode = iota
	// ipv4Always pins provider streams to IPv4 immediately.
	ipv4Always
	// ipv4Never keeps IPv6 and disarms the automatic fallback entirely.
	ipv4Never
)

// ipv4Setting is the operator's stance, read once.
var ipv4Setting = sync.OnceValue(func() ipv4Mode {
	return parseIPv4Mode(os.Getenv(forceIPv4Env))
})

// parseIPv4Mode reads the value of forceIPv4Env. Unrecognised text is treated as
// unset: a typo must not silently disarm a protection the operator was trying
// to turn on.
func parseIPv4Mode(raw string) ipv4Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return ipv4Always
	case "0", "false", "no", "off", "never":
		return ipv4Never
	default:
		return ipv4Auto
	}
}

// ipv6FallbackStrikes is how many IPv6-path failures are tolerated before the
// fallback trips. One is a blip — a single rotation, a momentary route loss.
// Two in one process is a pattern, and the second one has already cost the user
// a turn.
const ipv6FallbackStrikes = 2

var (
	ipv6Strikes  atomic.Int32
	ipv6FellBack atomic.Bool
)

// useIPv4 reports whether provider streams should be narrowed to IPv4 right now.
func useIPv4() bool {
	switch ipv4Setting() {
	case ipv4Always:
		return true
	case ipv4Never:
		return false
	}
	return ipv6FellBack.Load()
}

// isIPv6PathFailure reports whether err is the local IPv6 path giving out under
// a connection to an IPv6 peer, as opposed to any other way a stream can die.
//
// The signature is an unreachable-class errno on a socket whose REMOTE address
// is IPv6. That is what the kernel reports when the route to the peer is gone,
// and — the case that motivated this — when the temporary IPv6 source address
// the socket was bound to is rotated out from under an established connection.
// A peer that resets the connection says nothing about the address family, so
// it is deliberately not counted here.
func isIPv6PathFailure(err error) bool {
	if !errors.Is(err, syscall.EHOSTUNREACH) && !errors.Is(err, syscall.ENETUNREACH) {
		return false
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	addr, ok := opErr.Addr.(*net.TCPAddr)
	if !ok || addr.IP == nil {
		return false
	}
	// To4 returns non-nil for IPv4 and for v4-mapped addresses, both of which
	// travel over the IPv4 path and prove nothing about IPv6.
	return addr.IP.To4() == nil
}

// noteIPv6Failure records a stream death that looks like the IPv6 path failing,
// and trips the fallback once they stop looking like coincidence. It is called
// for MID-STREAM failures only: a connect-time failure is already handled by the
// dialer's own Happy Eyeballs, which tries IPv4 on its own, while a connection
// that dies after it was working is exactly what Happy Eyeballs cannot see.
func noteIPv6Failure(err error) {
	if ipv4Setting() != ipv4Auto || ipv6FellBack.Load() || !isIPv6PathFailure(err) {
		return
	}
	strikes := ipv6Strikes.Add(1)
	if strikes < ipv6FallbackStrikes {
		slog.Warn("provider stream died on the IPv6 path", "strikes", strikes, "err", err)
		return
	}
	// Narrowing to IPv4 on a host that has no IPv4 would turn an intermittent
	// failure into a total one. Check before committing to it.
	if !hasGlobalIPv4() {
		slog.Warn("IPv6 path failing repeatedly, but this host has no usable IPv4 address; staying on IPv6",
			"strikes", strikes, "env", forceIPv4Env)
		return
	}
	if ipv6FellBack.CompareAndSwap(false, true) {
		slog.Warn("provider streams falling back to IPv4 after repeated IPv6 failures",
			"strikes", strikes, "until", "process exit", "override", forceIPv4Env+"=off")
	}
}

// hasGlobalIPv4 reports whether this host has an IPv4 address worth dialing
// from — up, not loopback, not link-local.
func hasGlobalIPv4() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return true
	}
	return false
}

// streamDialer holds the connect-time bounds for provider streams.
var streamDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// dialStream opens the TCP connection for a provider request, narrowing to IPv4
// when the setting or the fallback calls for it. Only the unqualified "tcp" is
// narrowed: a caller that asked for tcp6 explicitly gets what it asked for
// rather than a silent contradiction.
func dialStream(ctx context.Context, network, addr string) (net.Conn, error) {
	if network == "tcp" && useIPv4() {
		network = "tcp4"
	}
	return streamDialer.DialContext(ctx, network, addr)
}

// idleWatchdog aborts a stream that stops producing data. It wraps the response
// body so the timer resets on bytes actually read off the wire, rather than once
// per parsed SSE line: the reader goroutine also spends time blocked handing
// events to the agent loop, and counting that as idle cancels healthy streams
// whenever the consumer is slow — which is exactly what happens on the large
// responses this is meant to protect.
type idleWatchdog struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
	fired   atomic.Bool
}

// newIdleWatchdog arms the watchdog with the caller's idle budget, which varies
// by endpoint — see streamIdleTimeoutBuffered.
func newIdleWatchdog(body io.Reader, cancel context.CancelFunc, timeout time.Duration) *idleWatchdog {
	if timeout <= 0 {
		timeout = streamIdleTimeout
	}
	w := &idleWatchdog{r: body, timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.fired.Store(true)
		cancel()
	})
	return w
}

func (w *idleWatchdog) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.timer.Reset(w.timeout)
	}
	return n, err
}

// Timeout is the idle budget this watchdog was armed with, so a report of the
// abort names the budget that actually applied rather than the default.
func (w *idleWatchdog) Timeout() time.Duration { return w.timeout }

// Fired reports whether the watchdog cancelled the request. It distinguishes "the
// connection went quiet" from "the caller aborted" — both of which surface as
// context.Canceled on the read.
func (w *idleWatchdog) Fired() bool { return w.fired.Load() }

func (w *idleWatchdog) Stop() { w.timer.Stop() }

// describeStreamReadError explains why a stream stopped part-way. Without it a
// failed scan is indistinguishable from a clean end of stream: the reader
// goroutine returns, the event channel closes, and the agent loop can only say
// the connection closed without a finish signal.
func describeStreamReadError(err error, idleFired bool, idleTimeout time.Duration) string {
	switch {
	case errors.Is(err, bufio.ErrTooLong):
		return fmt.Sprintf("stream read failed: provider sent a single SSE line larger than %d MB", streamMaxLineBytes/(1024*1024))
	case idleFired:
		return fmt.Sprintf("stream read failed: no data received for %s, connection appears stalled", idleTimeout)
	case errors.Is(err, context.Canceled):
		return "stream read failed: request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "stream read failed: request deadline exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "stream read failed: provider closed the connection mid-response"
	default:
		return "stream read failed: " + err.Error()
	}
}

type StreamEventType string

const (
	EventTextDelta     StreamEventType = "text-delta"
	EventToolCallStart StreamEventType = "tool-call-start"
	EventToolCallDelta StreamEventType = "tool-call-delta"
	EventToolCallEnd   StreamEventType = "tool-call-end"
	// EventReasoningStart opens a thinking block. It carries no content: it
	// marks the boundary between one block and the next, so blocks are stored
	// and replayed separately rather than concatenated.
	EventReasoningStart     StreamEventType = "reasoning-start"
	EventReasoning          StreamEventType = "reasoning"
	EventReasoningSignature StreamEventType = "reasoning-signature"
	// EventReasoningRedacted carries a safety-redacted thinking block. It has
	// no readable text — only an opaque payload that must be round-tripped
	// verbatim as a redacted_thinking block, so it is its own event rather
	// than a signature on an empty reasoning block.
	EventReasoningRedacted StreamEventType = "reasoning-redacted"
	EventFinish            StreamEventType = "finish"
	EventUsage             StreamEventType = "usage"
	EventError             StreamEventType = "error"
)

// TokenUsage carries per-message token accounting from a provider.
// Fields are non-zero where the provider reports them.
type TokenUsage struct {
	InputTokens      int `json:"inputTokens,omitempty"`
	OutputTokens     int `json:"outputTokens,omitempty"`
	ReasoningTokens  int `json:"reasoningTokens,omitempty"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

type StreamEvent struct {
	Type         StreamEventType `json:"type"`
	Text         string          `json:"text,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	RedactedData string          `json:"redactedData,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	ToolInput    json.RawMessage `json:"toolInput,omitempty"`
	FinishReason *string         `json:"finishReason,omitempty"`
	Usage        *TokenUsage     `json:"usage,omitempty"`
	Error        string          `json:"error,omitempty"`
	// Err is the Go error behind Error, carried in-process so a consumer can
	// match on the error's identity (errors.Is/As, down to the kernel errno)
	// instead of on its rendered text. Nil when the failure arrived as a
	// message from the provider rather than as a Go error. Never serialized:
	// the stream reader and the agent loop share a process, and the string is
	// what crosses any boundary beyond it.
	Err error `json:"-"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MessageImage is an image attached to a message, carried provider-neutrally.
// Data is base64-encoded image bytes; MediaType is e.g. "image/jpeg".
type MessageImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type ModelMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	// Images carries image attachments for a tool-result message. Providers
	// render these per their API: Anthropic embeds them in the tool_result
	// content block; OpenAI-family inject a follow-up user message.
	Images []MessageImage `json:"images,omitempty"`
	// ReasoningParts carries thinking/reasoning blocks from a previous assistant
	// turn. Anthropic requires these to be forwarded back as "thinking" content
	// blocks with their signatures intact; OpenAI-family providers handle
	// reasoning tokens server-side and should ignore this field.
	ReasoningParts []ReasoningPart `json:"reasoningParts,omitempty"`
}

// ReasoningPart represents a thinking/reasoning block from a model's response.
// Anthropic models return these with a cryptographic signature that must be
// forwarded back unchanged on subsequent turns.
type ReasoningPart struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
	// RedactedData is the opaque payload of a redacted_thinking block. When
	// set, the block carries no readable text and must be re-sent as a
	// redacted_thinking block rather than a thinking block.
	RedactedData string `json:"redactedData,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StreamRequest struct {
	Model       string           `json:"model"`
	System      []string         `json:"system"`
	Messages    []ModelMessage   `json:"messages"`
	Tools       []ToolDefinition `json:"tools"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"maxTokens,omitempty"`
	// Thinking asks for the model's reasoning mode, where the provider and the
	// model support one. Only the agent loop sets it. The short utility calls —
	// titles, the auto-mode risk gate, compaction — run on tight max_tokens
	// budgets that thinking would spend before reaching an answer, and none of
	// them is the kind of work reasoning improves.
	Thinking bool `json:"thinking,omitempty"`
	// CacheKey identifies the conversation this request belongs to, for
	// providers whose prompt cache is shared across machines and needs a routing
	// hint to land a session's requests on the node holding its prefix. Only the
	// agent loop sets it (to the session id); a provider that has no use for it
	// ignores it. It is a routing hint, never an identity — it must not carry
	// anything about the user.
	CacheKey string `json:"cacheKey,omitempty"`
	// Guidance is text the user sent mid-turn, already formatted and labelled by
	// the caller. It is carried beside Messages rather than folded into them
	// because the best place to put it differs by provider, and the wrong place
	// is expensive: the caller's fallback appends it to the turn's FIRST user
	// message, which is the earliest possible byte to change and therefore
	// discards the whole turn's cached history on the step right after the user
	// steers — the step they are waiting on.
	//
	// A provider that implements GuidancePlacer positions this itself and the
	// caller leaves Messages alone. Everyone else ignores the field and the
	// caller folds the same text into Messages as before.
	Guidance string `json:"guidance,omitempty"`
}

// GuidancePlacer is implemented by providers that position
// StreamRequest.Guidance themselves, at the end of the conversation where it
// costs no cached prefix. The caller checks for it before falling back to
// mutating the message history.
type GuidancePlacer interface {
	// PlacesGuidance reports whether this provider consumes
	// StreamRequest.Guidance.
	PlacesGuidance() bool
}

// PlacesGuidance reports whether p positions guidance itself.
func PlacesGuidance(p Provider) bool {
	gp, ok := p.(GuidancePlacer)
	return ok && gp.PlacesGuidance()
}

type ModelInfo struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ProviderID      string  `json:"providerId"`
	Default         bool    `json:"default"`
	ActiveByDefault bool    `json:"activeByDefault"`
	InputPricePerM  float64 `json:"inputPricePerM"`
	OutputPricePerM float64 `json:"outputPricePerM"`
	SupportsImages  bool    `json:"supportsImages"`
	// ContextWindow is the model's total context length in tokens (0 = unknown).
	// Used to size the compaction trigger; when 0 the loop falls back to a fixed
	// byte-size heuristic.
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxOutputTokens is the most output the model will produce in one response
	// (0 = unknown, leave the request's limit to the provider's own default).
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// Collection is an optional grouping label for dynamically-fetched models
	// from OpenAI-compatible providers (e.g. "DeepSeek", "Gemini") so the UI can
	// group them instead of collapsing everything under the OpenAI provider id.
	Collection string `json:"collection,omitempty"`
}

type Provider interface {
	ID() string
	Models() []ModelInfo
	StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error)
}

// ModelRefresher is an optional interface that providers can implement
// to support dynamic model list refreshing.
type ModelRefresher interface {
	RefreshModels()
}

type Registry struct {
	mu           sync.RWMutex // protects providers
	providers    map[string]Provider
	customModels map[string]string // modelID -> providerID
	customMu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		providers:    make(map[string]Provider),
		customModels: make(map[string]string),
	}
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	r.providers[p.ID()] = p
	r.mu.Unlock()
}

func (r *Registry) Get(id string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[id]
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}

// snapshot returns the registered providers as a slice under a read lock, so
// callers can iterate and call Models() (which may hit the network) without
// holding the registry lock or racing with ReplaceProviders.
func (r *Registry) snapshot() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		ps = append(ps, p)
	}
	return ps
}

func (r *Registry) ListModels() []ModelInfo {
	var models []ModelInfo
	for _, p := range r.snapshot() {
		models = append(models, p.Models()...)
	}
	return models
}

// ModelSupportsImages reports whether the given model accepts image input.
// Unknown models default to false.
func (r *Registry) ModelSupportsImages(modelID string) bool {
	if modelID == "" {
		return false
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.SupportsImages
			}
		}
	}
	return false
}

// ContextWindow returns the model's total context length in tokens, or 0 when
// unknown (dynamically-fetched models without catalog metadata). Callers treat
// 0 as "fall back to a size heuristic".
func (r *Registry) ContextWindow(modelID string) int {
	if modelID == "" {
		return 0
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.ContextWindow
			}
		}
	}
	return 0
}

// MaxOutputTokens returns the model's output ceiling in tokens, or 0 when
// unknown. Callers treat 0 as "send no explicit limit and let the provider
// apply its own default" — overstating a ceiling makes every request fail, so
// unknown must never be guessed upward.
func (r *Registry) MaxOutputTokens(modelID string) int {
	if modelID == "" {
		return 0
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.MaxOutputTokens
			}
		}
	}
	return 0
}

func (r *Registry) RegisterCustomModel(modelID, providerID string) {
	r.customMu.Lock()
	r.customModels[modelID] = providerID
	r.customMu.Unlock()
}

func (r *Registry) UnregisterCustomModel(modelID string) {
	r.customMu.Lock()
	delete(r.customModels, modelID)
	r.customMu.Unlock()
}

// IsCustomModel reports whether modelID was registered as a user-added custom
// model (rather than a built-in catalog or dynamically-fetched model). Custom
// models are not present in any provider's curated catalog, so capability
// lookups that trust the catalog must treat them as unknown and probe instead.
func (r *Registry) IsCustomModel(modelID string) bool {
	r.customMu.RLock()
	defer r.customMu.RUnlock()
	_, ok := r.customModels[modelID]
	return ok
}

func (r *Registry) ResolveProvider(modelID string) Provider {
	// Check custom model routing first
	r.customMu.RLock()
	providerID, customOk := r.customModels[modelID]
	r.customMu.RUnlock()
	if customOk {
		if p := r.Get(providerID); p != nil {
			return p
		}
	}
	ps := r.snapshot()
	// Then check built-in models
	for _, p := range ps {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return p
			}
		}
	}
	// Fallback to first provider
	for _, p := range ps {
		return p
	}
	return nil
}

// NewProviderWithConfig creates a Provider with explicit credentials, used when
// credentials come from the DB rather than environment variables.
// providerID must be "anthropic", "openai", "openrouter", or "ollama".
// Env-var values are used as the base; apiKey and baseURL override them when non-empty.
func NewProviderWithConfig(providerID, apiKey, baseURL string) (Provider, error) {
	switch providerID {
	case "anthropic":
		p := NewAnthropicProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
		}
		return p, nil
	case "openai":
		p := NewOpenAIProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
			p.collection = collectionFromBaseURL(baseURL)
		}
		return p, nil
	case "openrouter":
		p := NewOpenRouterProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		return p, nil
	case "ollama":
		p := NewOllamaProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider %q; must be anthropic, openai, openrouter, or ollama", providerID)
	}
}

// RefreshModels clears cached model lists for all providers that support it,
// forcing re-fetch on next Models() call.
func (r *Registry) RefreshModels() {
	for _, p := range r.snapshot() {
		if refresher, ok := p.(ModelRefresher); ok {
			refresher.RefreshModels()
		}
	}
}

// ProviderPriority is the stable order used to choose a default provider when a
// session does not specify a model.
//
// User-configured first-party providers always win. Free-tier providers
// (keyed "ogcode-<collection>") are appended so the app works out-of-the-box
// with the community key pool, but never override a user's own credentials.
var ProviderPriority = []string{
	"anthropic", "openai", "openrouter", "ollama",
	"ogcode-openrouter", "ogcode-cerebras", "ogcode-sambanova",
	"ogcode-github_models", "ogcode-nvidia",
}

// Default returns the highest-priority registered provider, or nil if the
// registry has no providers.
func (r *Registry) Default() Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range ProviderPriority {
		if p, ok := r.providers[id]; ok {
			return p
		}
	}
	for _, p := range r.providers {
		return p
	}
	return nil
}

// DefaultUsable returns the provider a fresh prompt should run on, applying the
// same priority as Default but refusing to hand back an installed-but-stopped
// Ollama when anything else is available. A registered ollama provider only
// means "the binary exists (or a base URL was saved)" — if the daemon is down
// and OLLAMA_API_KEY is unset, the first prompt would die with connection
// refused, and ollama's presence in ProviderPriority would otherwise shadow the
// community free pool and every usable provider behind it.
//
// An ollama provider is considered usable when OLLAMA_API_KEY is set or the
// daemon answers a probe. Non-ollama providers are always considered usable;
// unknown ollama implementations are assumed usable, while the concrete
// *OpenAIProvider is probed. When ollama is the only registered provider it is
// returned even if unreachable — ollama-only users otherwise lose their only
// option, and the provider surfaces the real connection error itself.
func (r *Registry) DefaultUsable() Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Priority walk, mirroring Default: first registered non-ollama wins; a
	// registered ollama is remembered as the default-candidate and only
	// returned if nothing usable outranks it (or it proves usable itself).
	var ollama Provider
	for _, id := range ProviderPriority {
		if p, ok := r.providers[id]; ok {
			if id == "ollama" {
				ollama = p
				break
			}
			return p
		}
	}
	if ollama == nil {
		// Default's fallback: the first map entry. Skip ollama there too so a
		// non-priority provider outranks a stopped daemon.
		for _, p := range r.providers {
			if p.ID() == "ollama" {
				ollama = p
				continue
			}
			return p
		}
	}
	if ollama == nil {
		return nil // empty registry
	}

	// The default resolved to ollama. Unknown implementations are assumed
	// usable; the concrete *OpenAIProvider is probed.
	o, isOpenAI := ollama.(*OpenAIProvider)
	if !isOpenAI || o == nil {
		return ollama
	}
	if os.Getenv("OLLAMA_API_KEY") != "" || OllamaRunning(o.BaseURL()) {
		return ollama
	}

	// Daemon down and unauthenticated: yield to the next usable provider.
	for _, id := range ProviderPriority {
		if id == "ollama" {
			continue
		}
		if p, ok := r.providers[id]; ok {
			return p
		}
	}
	for _, p := range r.providers {
		if p.ID() != "ollama" {
			return p
		}
	}
	return ollama
}

// ReplaceProviders atomically swaps the set of registered providers. Custom
// model routing (RegisterCustomModel) is preserved. Used to apply provider
// credential changes from the settings/onboarding UI without a server restart.
func (r *Registry) ReplaceProviders(providers map[string]Provider) {
	r.mu.Lock()
	r.providers = providers
	r.mu.Unlock()
}
