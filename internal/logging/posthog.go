package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// Shipping logs to PostHog Logs. PostHog ingests OpenTelemetry logs at
// /i/v1/logs, authenticated with the project token as a bearer credential.
// ogcode speaks OTLP/HTTP JSON directly — the same "no SDK dependency"
// approach the analytics client in internal/server takes — so shipping logs
// adds no module to the build.
var (
	// Shipped through the managed reverse proxy on ogcode.in for the same
	// reason the capture and decide calls are: *.posthog.com is often blocked.
	posthogLogsEndpoint = "https://a.ogcode.in/i/v1/logs"
	posthogLogsToken    = "phc_CGzEmfPURHyNWrG49yNJA7wY5io8URFu3sazRYTAXw6Z"
	posthogLogsClient   = &http.Client{Timeout: 10 * time.Second}
)

// Shipping is on by default: OGCODE_POSTHOG_LOGS turns it off, OGCODE_POSTHOG_LOGS_LEVEL
// raises or lowers the severity floor, and DO_NOT_TRACK disables it whatever the
// flag says.
const (
	posthogLogsFlagEnv  = "OGCODE_POSTHOG_LOGS"
	posthogLogsLevelEnv = "OGCODE_POSTHOG_LOGS_LEVEL"

	posthogQueueSize     = 512
	posthogBatchMax      = 100
	posthogFlushInterval = 5 * time.Second

	posthogQueueWarnEvery = time.Minute // rate limit of the stderr queue-full warning
)

// posthogLogsEnabled reads OGCODE_POSTHOG_LOGS. Shipping is on by default: only
// an explicit off turns it off, and DO_NOT_TRACK turns it off regardless.
func posthogLogsEnabled() bool {
	if doNotTrack() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(posthogLogsFlagEnv))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// doNotTrack reports whether DO_NOT_TRACK asks for no telemetry. Set to any
// value other than 0/false/no/off is a request to opt out, per the
// https://consoledonottrack.com convention.
func doNotTrack() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DO_NOT_TRACK"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// posthogLogsLevel reads OGCODE_POSTHOG_LOGS_LEVEL. Unset is warn.
func posthogLogsLevel() slog.Level {
	if l, ok := parseLevel(os.Getenv(posthogLogsLevelEnv)); ok {
		return l
	}
	return slog.LevelWarn
}

// hostName is this machine's name, or "" when it cannot be read. It names the
// process in the startup line and, prefixed, the person its logs attach to.
func hostName() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}

// posthogLogsDistinctID is the anonymous person these logs attach to. It is the
// same "server-<hostname>" id the analytics client uses (internal/server's
// posthogDistinctID), so a machine's logs and its events land on one person.
func posthogLogsDistinctID() string {
	if h := hostName(); h != "" {
		return "server-" + h
	}
	return "ogcode-server"
}

func posthogResource(o Options) []otlpAttribute {
	cmd := strings.TrimSuffix(o.Name, ".log")
	if cmd == "" {
		cmd = "ogcode"
	}
	pid := os.Getpid()
	return []otlpAttribute{
		strAttr("service.name", "ogcode"),
		strAttr("service.version", version.Version),
		strAttr("process.command", cmd),
		otlpAttribute{Key: "process.pid", Value: intValue(int64(pid))},
		strAttr("service.instance.id", hostName()+"-"+strconv.Itoa(pid)),
		strAttr("posthogDistinctId", posthogLogsDistinctID()),
	}
}

// posthogSink buffers shipped records and POSTs them in batches. It is
// best-effort: a failed send is dropped, never retried — but counted, and a
// rate-limited stderr warning says when the queue is overflowing. The file
// log remains the durable record.
type posthogSink struct {
	level    slog.Level
	endpoint string
	token    string
	client   *http.Client
	resource []otlpAttribute

	queue    chan posthogRecord
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	stats   shipCounters
	stopped atomic.Bool
	// lastQueueWarn is the unixnano time of the last stderr queue-full
	// warning, rate limited to posthogQueueWarnEvery.
	lastQueueWarn atomic.Int64
}

type posthogRecord struct {
	rec   slog.Record
	attrs []slog.Attr
}

// shipCounters is the sink's tallies, one field per way a batch or a record
// can be lost. Everything is atomic: Handle, the runner and stop() bump them
// from different goroutines, and Close reads them for the summary.
type shipCounters struct {
	sent             atomic.Int64 // batches the endpoint answered 2xx
	droppedEncode    atomic.Int64 // batches json.Marshal refused
	droppedRequest   atomic.Int64 // batches whose http.Request could not be built
	droppedTransport atomic.Int64 // batches client.Do failed on
	droppedStatus    atomic.Int64 // batches answered with a non-2xx status
	droppedQueue     atomic.Int64 // records dropped for a full queue or a stopped sink
	unflushed        atomic.Int64 // records still queued when the sink stopped
}

// ShipStats is what a run of the shipping sink did. The Dropped* fields count
// batches (or, for DroppedQueue, records) by the failure that lost them;
// Unflushed counts records still queued when the sink was stopped. This is
// what tells a support engineer "PostHog is dropping everything" from "a
// quiet day".
type ShipStats struct {
	Enabled          bool
	Queued           int64
	Sent             int64
	DroppedQueue     int64
	DroppedEncode    int64
	DroppedRequest   int64
	DroppedTransport int64
	DroppedStatus    int64
	Unflushed        int64
}

// snapshot reads the sink's tallies out in one pass. Reading a channel's
// length is safe against concurrent sends.
func (s *posthogSink) snapshot() ShipStats {
	return ShipStats{
		Enabled:          true,
		Queued:           int64(len(s.queue)),
		Sent:             s.stats.sent.Load(),
		DroppedQueue:     s.stats.droppedQueue.Load(),
		DroppedEncode:    s.stats.droppedEncode.Load(),
		DroppedRequest:   s.stats.droppedRequest.Load(),
		DroppedTransport: s.stats.droppedTransport.Load(),
		DroppedStatus:    s.stats.droppedStatus.Load(),
		Unflushed:        s.stats.unflushed.Load(),
	}
}

// posthogWarnWriter is where queue-full warnings go — written directly, never
// through slog, which from Handle would re-enter this sink. A var so a test
// can capture the line.
var posthogWarnWriter io.Writer = os.Stderr

// posthogDrainDeadline bounds how long stop() waits for the runner to flush
// the queue. Emptying a full queue at the request timeout can take a minute;
// process exit must not hang behind it. A var so a test can shrink it.
var posthogDrainDeadline = 5 * time.Second

func newPostHogSink(level slog.Level, endpoint, token string, client *http.Client, flush time.Duration, o Options) *posthogSink {
	s := &posthogSink{
		level:    level,
		endpoint: endpoint,
		token:    token,
		client:   client,
		resource: posthogResource(o),
		queue:    make(chan posthogRecord, posthogQueueSize),
		done:     make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run(flush)
	return s
}

// stop closes the sink: after it, Handle counts records as dropped rather
// than park them in a dead queue. The runner gets posthogDrainDeadline to
// flush what it can; whatever is still queued when that fires counts as
// unflushed instead of hanging process exit.
func (s *posthogSink) stop() {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		close(s.done)
	})
	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(posthogDrainDeadline):
	}
	if left := len(s.queue); left > 0 {
		s.stats.unflushed.Add(int64(left))
	}
}

func (s *posthogSink) run(interval time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	batch := make([]posthogRecord, 0, posthogBatchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.send(batch)
		batch = batch[:0]
	}
	for {
		select {
		case rec := <-s.queue:
			batch = append(batch, rec)
			if len(batch) >= posthogBatchMax {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			for {
				select {
				case rec := <-s.queue:
					batch = append(batch, rec)
					if len(batch) >= posthogBatchMax {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// send POSTs one batch of records. It is best-effort: any failure drops the
// batch, never retried — but counted, so a rotated token (401) or a failing
// endpoint (413/429/5xx) shows up in ShipStats and the Close summary instead
// of vanishing.
func (s *posthogSink) send(batch []posthogRecord) {
	payload := otlpLogsRequest{
		ResourceLogs: []otlpResourceLogs{{
			Resource: otlpResource{Attributes: s.resource},
			ScopeLogs: []otlpScopeLogs{{
				Scope:      otlpScope{Name: "ogcode"},
				LogRecords: buildLogRecords(batch),
			}},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.stats.droppedEncode.Add(1)
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		s.stats.droppedRequest.Add(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		s.stats.droppedTransport.Add(1)
		return
	}
	// Drain the body before closing so the connection can be reused,
	// whatever the status turned out to be.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		s.stats.droppedStatus.Add(1)
		return
	}
	s.stats.sent.Add(1)
}

// posthogHandler is the slog.Handler wrapper around a sink. It carries only the
// attributes added with WithAttrs, so a copy never duplicates the sink's locks.
type posthogHandler struct {
	sink  *posthogSink
	attrs []slog.Attr
}

func (h *posthogHandler) Enabled(_ context.Context, l slog.Level) bool { return h.sink.level <= l }

func (h *posthogHandler) Handle(_ context.Context, r slog.Record) error {
	if h.sink.level > r.Level {
		return nil
	}
	s := h.sink
	if s.stopped.Load() {
		// Nothing will drain the queue after Close; count the record rather
		// than park it in a dead channel.
		s.stats.droppedQueue.Add(1)
		return nil
	}
	select {
	case s.queue <- posthogRecord{rec: r.Clone(), attrs: h.attrs}:
	default:
		s.stats.droppedQueue.Add(1)
		// Warn on stderr, rate limited to once a minute: silent loss is how
		// this sink was found broken in the first place. Written directly —
		// slog from here would re-enter the sink.
		now := time.Now().UnixNano()
		if last := s.lastQueueWarn.Load(); last == 0 || now-last >= int64(posthogQueueWarnEvery) {
			if s.lastQueueWarn.CompareAndSwap(last, now) {
				fmt.Fprintf(posthogWarnWriter,
					"ogcode: posthog log shipping queue full; %d records dropped so far (queue size %d)\n",
					s.stats.droppedQueue.Load(), len(s.queue))
			}
		}
	}
	return nil
}

func (h *posthogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	cp := &posthogHandler{sink: h.sink, attrs: make([]slog.Attr, 0, len(h.attrs)+len(attrs))}
	cp.attrs = append(cp.attrs, h.attrs...)
	cp.attrs = append(cp.attrs, attrs...)
	return cp
}

// WithGroup flattens: group nesting is not reproduced, which keeps this
// best-effort sink simple and costs nothing for the flat records ogcode logs.
func (h *posthogHandler) WithGroup(string) slog.Handler { return h }

// buildLogRecords converts buffered records to OTLP log records, redacting
// secrets on the way out exactly as the file handler does.
func buildLogRecords(batch []posthogRecord) []otlpLogRecord {
	recs := make([]otlpLogRecord, 0, len(batch))
	for _, br := range batch {
		r := br.rec
		ts := r.Time
		if ts.IsZero() {
			ts = time.Now()
		}
		nanos := strconv.FormatInt(ts.UnixNano(), 10)
		sevNum, sevText := otlpSeverity(r.Level)

		attrs := make([]otlpAttribute, 0, len(br.attrs)+r.NumAttrs()+2)
		for _, a := range br.attrs {
			attrs = appendRedacted(attrs, "", a)
		}
		r.Attrs(func(a slog.Attr) bool {
			attrs = appendRedacted(attrs, "", a)
			return true
		})
		if r.PC != 0 {
			if fr, _ := runtime.CallersFrames([]uintptr{r.PC}).Next(); fr.File != "" {
				// The basename only: a user's home or build directory must not
				// leave the machine on every record. code.lineno still does.
				attrs = append(attrs,
					strAttr("code.filepath", filepath.Base(fr.File)),
					otlpAttribute{Key: "code.lineno", Value: intValue(int64(fr.Line))},
				)
			}
		}

		recs = append(recs, otlpLogRecord{
			TimeUnixNano:         nanos,
			ObservedTimeUnixNano: nanos,
			SeverityNumber:       sevNum,
			SeverityText:         sevText,
			Body:                 stringValue(Scrub(r.Message)),
			Attributes:           attrs,
		})
	}
	return recs
}

// appendRedacted adds an attribute, blanking secret-named keys and scrubbing
// secret shapes out of values. Groups flatten to dotted keys.
func appendRedacted(dst []otlpAttribute, prefix string, a slog.Attr) []otlpAttribute {
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, sub := range a.Value.Group() {
			dst = appendRedacted(dst, key, sub)
		}
		return dst
	}
	return append(dst, otlpAttribute{Key: key, Value: attrValue(redactAttr(nil, a))})
}

// otlpSeverity maps a slog level to its OTLP severity number and text.
func otlpSeverity(l slog.Level) (int, string) {
	switch {
	case l >= slog.LevelError+4:
		return 21, "FATAL"
	case l >= slog.LevelError:
		return 17, "ERROR"
	case l >= slog.LevelWarn+4:
		return 15, "WARN3"
	case l >= slog.LevelWarn:
		return 13, "WARN"
	case l >= slog.LevelInfo+4:
		return 11, "INFO2"
	case l >= slog.LevelInfo:
		return 9, "INFO"
	case l >= slog.LevelDebug:
		return 5, "DEBUG"
	default:
		return 1, "TRACE"
	}
}

// --- OTLP/HTTP JSON wire types (proto3 JSON field names) ---

type otlpLogsRequest struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpLogRecord struct {
	TimeUnixNano         string          `json:"timeUnixNano"`
	ObservedTimeUnixNano string          `json:"observedTimeUnixNano"`
	SeverityNumber       int             `json:"severityNumber"`
	SeverityText         string          `json:"severityText"`
	Body                 otlpValue       `json:"body"`
	Attributes           []otlpAttribute `json:"attributes,omitempty"`
}

type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

// otlpValue is the AnyValue encoding. int64 and uint64 ride as strings, the
// proto3 JSON convention.
type otlpValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

func strAttr(key, value string) otlpAttribute {
	return otlpAttribute{Key: key, Value: stringValue(value)}
}

func stringValue(s string) otlpValue { return otlpValue{StringValue: &s} }

func intValue(n int64) otlpValue {
	s := strconv.FormatInt(n, 10)
	return otlpValue{IntValue: &s}
}

func uintValue(n uint64) otlpValue {
	s := strconv.FormatUint(n, 10)
	return otlpValue{IntValue: &s}
}

func attrValue(a slog.Attr) otlpValue {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return stringValue(v.String())
	case slog.KindInt64:
		return intValue(v.Int64())
	case slog.KindUint64:
		return uintValue(v.Uint64())
	case slog.KindFloat64:
		f := v.Float64()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			// NaN and ±Inf cannot ride as doubles: proto3 JSON cannot encode
			// them, so json.Marshal would fail and silently discard the whole
			// batch. The string form survives instead.
			return stringValue(strconv.FormatFloat(f, 'g', -1, 64))
		}
		return otlpValue{DoubleValue: &f}
	case slog.KindBool:
		b := v.Bool()
		return otlpValue{BoolValue: &b}
	case slog.KindDuration:
		return stringValue(v.Duration().String())
	case slog.KindTime:
		return stringValue(v.Time().UTC().Format(time.RFC3339Nano))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case nil:
			return stringValue("")
		case error:
			return stringValue(x.Error())
		case fmt.Stringer:
			return stringValue(x.String())
		default:
			return stringValue(fmt.Sprintf("%+v", x))
		}
	default:
		return stringValue(v.String())
	}
}
