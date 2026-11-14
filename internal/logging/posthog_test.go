package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostHogLogsEnabled(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true}, // on by default: unset and empty ship
		{"1", true},
		{"true", true},
		{"yes", true},
		{"ON", true},
		{"on", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"FALSE", false},
		{" Off ", false},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv("DO_NOT_TRACK", "")
			t.Setenv(posthogLogsFlagEnv, c.value)
			if got := posthogLogsEnabled(); got != c.want {
				t.Errorf("posthogLogsEnabled(%q) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

// DO_NOT_TRACK disables shipping whatever the flag says.
func TestPostHogLogsRespectsDoNotTrack(t *testing.T) {
	t.Setenv(posthogLogsFlagEnv, "1")
	for _, v := range []string{"1", "true", "yes", "on"} {
		t.Setenv("DO_NOT_TRACK", v)
		if posthogLogsEnabled() {
			t.Errorf("DO_NOT_TRACK=%q should disable shipping", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off"} {
		t.Setenv("DO_NOT_TRACK", v)
		if !posthogLogsEnabled() {
			t.Errorf("DO_NOT_TRACK=%q should leave the flag in charge", v)
		}
	}
}

func TestPostHogLogsLevel(t *testing.T) {
	t.Setenv(posthogLogsLevelEnv, "")
	if got := posthogLogsLevel(); got != slog.LevelWarn {
		t.Errorf("unset level = %v, want warn", got)
	}
	t.Setenv(posthogLogsLevelEnv, "debug")
	if got := posthogLogsLevel(); got != slog.LevelDebug {
		t.Errorf("debug level = %v, want debug", got)
	}
	t.Setenv(posthogLogsLevelEnv, "error")
	if got := posthogLogsLevel(); got != slog.LevelError {
		t.Errorf("error level = %v, want error", got)
	}
}

// The remote floor follows its own knob alone: a debug file level must not
// lower it below warn, and the env can still raise it.
func TestPostHogShipLevelClampsToRemoteFloor(t *testing.T) {
	t.Setenv(posthogLogsLevelEnv, "")
	if got := posthogShipLevel(); got != DefaultRemoteLevel {
		t.Errorf("unset ship level = %v, want %v", got, DefaultRemoteLevel)
	}
	t.Setenv(posthogLogsLevelEnv, "debug")
	if got := posthogShipLevel(); got != DefaultRemoteLevel {
		t.Errorf("debug ship level = %v, want %v (a debug file must not ship debug)", got, DefaultRemoteLevel)
	}
	t.Setenv(posthogLogsLevelEnv, "error")
	if got := posthogShipLevel(); got != slog.LevelError {
		t.Errorf("error ship level = %v, want error", got)
	}
}

func TestOTLPSeverity(t *testing.T) {
	cases := []struct {
		level slog.Level
		num   int
		text  string
	}{
		{slog.LevelError + 4, 21, "FATAL"},
		{slog.LevelError, 17, "ERROR"},
		{slog.LevelWarn, 13, "WARN"},
		{slog.LevelInfo, 9, "INFO"},
		{slog.LevelDebug, 5, "DEBUG"},
		{slog.LevelDebug - 4, 1, "TRACE"},
	}
	for _, c := range cases {
		num, text := otlpSeverity(c.level)
		if num != c.num || text != c.text {
			t.Errorf("otlpSeverity(%v) = (%d, %q), want (%d, %q)", c.level, num, text, c.num, c.text)
		}
	}
}

func TestBuildLogRecordsShape(t *testing.T) {
	ts := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	r := slog.NewRecord(ts, slog.LevelError, "turn failed", 0)
	r.AddAttrs(slog.String("session", "s1"))

	recs := buildLogRecords([]posthogRecord{{rec: r}})
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	got := recs[0]
	if got.TimeUnixNano != strconv.FormatInt(ts.UnixNano(), 10) {
		t.Errorf("timeUnixNano = %q, want %d", got.TimeUnixNano, ts.UnixNano())
	}
	if got.ObservedTimeUnixNano != got.TimeUnixNano {
		t.Errorf("observedTimeUnixNano = %q, want %q", got.ObservedTimeUnixNano, got.TimeUnixNano)
	}
	if got.SeverityNumber != 17 || got.SeverityText != "ERROR" {
		t.Errorf("severity = (%d, %q), want (17, ERROR)", got.SeverityNumber, got.SeverityText)
	}
	if got.Body.StringValue == nil || *got.Body.StringValue != "turn failed" {
		t.Errorf("body = %+v, want \"turn failed\"", got.Body)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Key != "session" {
		t.Fatalf("attributes = %+v, want one session attr", got.Attributes)
	}
	if got.Attributes[0].Value.StringValue == nil || *got.Attributes[0].Value.StringValue != "s1" {
		t.Errorf("session value = %+v, want s1", got.Attributes[0].Value)
	}
}

func TestBuildLogRecordsRedactsSecrets(t *testing.T) {
	r := slog.NewRecord(time.Now(), slog.LevelError, "auth failed sk-ant-api03-abcdefghijklmnop", 0)
	r.AddAttrs(slog.String("api_key", "sk-ant-secretvalue1234567890"))

	recs := buildLogRecords([]posthogRecord{{rec: r}})
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	got := recs[0]
	body := ""
	if got.Body.StringValue != nil {
		body = *got.Body.StringValue
	}
	if strings.Contains(body, "sk-ant-") {
		t.Errorf("body still carries the secret: %q", body)
	}
	if !strings.Contains(body, Redacted) {
		t.Errorf("body was not scrubbed: %q", body)
	}
	var found bool
	for _, a := range got.Attributes {
		if a.Key != "api_key" {
			continue
		}
		found = true
		if a.Value.StringValue == nil || *a.Value.StringValue != Redacted {
			t.Errorf("api_key attr = %+v, want %s", a.Value, Redacted)
		}
	}
	if !found {
		t.Errorf("api_key attribute missing from %+v", got.Attributes)
	}
}

func TestShipsLogs(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv(posthogLogsFlagEnv, "")
	if !(Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("shipping is on by default; a named logger should ship")
	}
	if (Options{}).shipsLogs() {
		t.Error("a logger without a file should not ship logs")
	}
	t.Setenv(posthogLogsFlagEnv, "on")
	if !(Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("OGCODE_POSTHOG_LOGS=on should keep shipping")
	}
	t.Setenv(posthogLogsFlagEnv, "off")
	if (Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("OGCODE_POSTHOG_LOGS=off should stop shipping")
	}
	t.Setenv(posthogLogsFlagEnv, "")
	t.Setenv("DO_NOT_TRACK", "1")
	if (Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("DO_NOT_TRACK should stop shipping the default-on sink")
	}
	t.Setenv("DO_NOT_TRACK", "")
	if (Options{Name: "ogcode.log", Level: LevelOff}).shipsLogs() {
		t.Error("an off file level should silence shipping")
	}
}

// An off remote level means no shipping at all: no handler is registered, so
// no flush goroutine is started either.
func TestOffLevelWithholdsShipping(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv(posthogLogsFlagEnv, "1")
	t.Setenv(posthogLogsLevelEnv, "off")
	if (Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("an off ship level should withhold shipping")
	}
	l := New(Options{
		Name:    "ogcode.log",
		Dir:     t.TempDir(),
		Level:   slog.LevelInfo,
		Format:  "text",
		Console: LevelOff,
		Rotate:  RotateOptions{MaxSize: 1 << 20},
		Stderr:  io.Discard,
	})
	defer l.Close()
	if l.ship != nil {
		t.Error("an off ship level still built a sink and its goroutine")
	}
}

// The sink buffers records and POSTs them as OTLP/HTTP JSON with the project
// token as a bearer credential.
func TestPostHogSinkSendsBatch(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []*http.Request
		bodies   [][]byte
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, r.Clone(r.Context()))
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// A long flush interval means the only send is the final drain on stop().
	sink := newPostHogSink(slog.LevelWarn, ts.URL, "tok", ts.Client(), time.Hour, Options{Name: "ogcode.log"})
	logger := slog.New(&posthogHandler{sink: sink})

	logger.Warn("worth shipping", "session", "s1")
	logger.Info("below the floor")
	sink.stop()

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("want exactly 1 request, got %d", len(requests))
	}
	req := requests[0]
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var payload otlpLogsRequest
	if err := json.Unmarshal(bodies[0], &payload); err != nil {
		t.Fatalf("body is not OTLP JSON: %v\n%s", err, bodies[0])
	}
	if len(payload.ResourceLogs) != 1 {
		t.Fatalf("want 1 resourceLogs entry, got %d", len(payload.ResourceLogs))
	}
	rl := payload.ResourceLogs[0]
	if len(rl.ScopeLogs) != 1 || rl.ScopeLogs[0].Scope.Name != "ogcode" {
		t.Fatalf("unexpected scope: %+v", rl.ScopeLogs)
	}
	// A service.name resource attribute pins what these logs are.
	var hasService bool
	for _, a := range rl.Resource.Attributes {
		if a.Key == "service.name" && a.Value.StringValue != nil && *a.Value.StringValue == "ogcode" {
			hasService = true
		}
	}
	if !hasService {
		t.Errorf("resource lacks service.name=ogcode: %+v", rl.Resource.Attributes)
	}
	recs := rl.ScopeLogs[0].LogRecords
	if len(recs) != 1 {
		t.Fatalf("want 1 log record (the warn), got %d", len(recs))
	}
	if recs[0].Body.StringValue == nil || *recs[0].Body.StringValue != "worth shipping" {
		t.Errorf("record body = %+v, want \"worth shipping\"", recs[0].Body)
	}
}

// Shipping carries only the file's basename: a user's home or build directory
// must not leave the machine on every record. code.lineno stays.
func TestBuildLogRecordsShipsFileBasename(t *testing.T) {
	pc, file, _, _ := runtime.Caller(0)
	r := slog.NewRecord(time.Now(), slog.LevelError, "boom", pc)
	recs := buildLogRecords([]posthogRecord{{rec: r}})
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	var gotFile, gotLine string
	for _, a := range recs[0].Attributes {
		switch a.Key {
		case "code.filepath":
			if a.Value.StringValue != nil {
				gotFile = *a.Value.StringValue
			}
		case "code.lineno":
			if a.Value.IntValue != nil {
				gotLine = *a.Value.IntValue
			}
		}
	}
	if gotFile == "" {
		t.Fatalf("record lacks code.filepath: %+v", recs[0].Attributes)
	}
	if want := filepath.Base(file); gotFile != want {
		t.Errorf("code.filepath = %q, want the basename %q", gotFile, want)
	}
	if gotLine == "" {
		t.Errorf("record lacks code.lineno: %+v", recs[0].Attributes)
	}
}

// The resource names the shipping process — which binary (the log file's name
// minus .log), which pid, which instance — and keeps the old attributes. It
// never ships a directory or a full path.
func TestPostHogResource(t *testing.T) {
	attrs := map[string]otlpValue{}
	for _, a := range posthogResource(Options{Name: "ogcode.log"}) {
		attrs[a.Key] = a.Value
	}
	pid := os.Getpid()
	cases := []struct {
		key  string
		want string // want of the stringValue, except process.pid
	}{
		{"service.name", "ogcode"},
		{"process.command", "ogcode"},
		{"service.instance.id", hostName() + "-" + strconv.Itoa(pid)},
	}
	for _, c := range cases {
		v := attrs[c.key].StringValue
		if v == nil || *v != c.want {
			t.Errorf("%s = %+v, want %q", c.key, attrs[c.key], c.want)
		}
	}
	if v := attrs["process.pid"].IntValue; v == nil || *v != strconv.Itoa(pid) {
		t.Errorf("process.pid = %+v, want %d", attrs["process.pid"], pid)
	}
	if attrs["service.version"].StringValue == nil || attrs["posthogDistinctId"].StringValue == nil {
		t.Errorf("resource lost its identity attributes: %+v", attrs)
	}
	for k, v := range attrs {
		if v.StringValue != nil && strings.Contains(*v.StringValue, string(filepath.Separator)) {
			t.Errorf("resource attribute %q ships a path: %q", k, *v.StringValue)
		}
	}
}

// A non-finite float cannot ride as a double — proto3 JSON cannot encode it,
// and json.Marshal would fail and silently discard the whole batch — so it
// ships as its string form instead. A finite float still rides as a double.
func TestAttrValueEncodesNonFiniteFloats(t *testing.T) {
	cases := []struct {
		name  string
		f     float64
		asStr bool
		str   string
	}{
		{"NaN", math.NaN(), true, "NaN"},
		{"+Inf", math.Inf(1), true, "+Inf"},
		{"-Inf", math.Inf(-1), true, "-Inf"},
		{"finite", 1.5, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := attrValue(slog.Float64("ratio", c.f))
			if c.asStr {
				if v.DoubleValue != nil || v.StringValue == nil || *v.StringValue != c.str {
					t.Errorf("attrValue(%v) = %+v, want stringValue %q", c.f, v, c.str)
				}
			} else if v.DoubleValue == nil || *v.DoubleValue != c.f || v.StringValue != nil {
				t.Errorf("attrValue(%v) = %+v, want doubleValue %v", c.f, v, c.f)
			}
			// The whole batch still marshals, whatever the float was.
			r := slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)
			r.AddAttrs(slog.Float64("ratio", c.f))
			payload := otlpLogsRequest{ResourceLogs: []otlpResourceLogs{{
				ScopeLogs: []otlpScopeLogs{{LogRecords: buildLogRecords([]posthogRecord{{rec: r}})}},
			}}}
			if b, err := json.Marshal(payload); err != nil {
				t.Fatalf("payload did not marshal: %v", err)
			} else if !bytes.Contains(b, []byte("ratio")) {
				t.Errorf("marshalled payload lost the attribute: %s", b)
			}
		})
	}
}

// Every way a batch can be lost is counted, and only 2xx is success. A 401 is
// a rotated or revoked token; 413/429/5xx are transient. None are retried —
// best-effort — but none are silent either.
func TestSendCountsEveryResponse(t *testing.T) {
	prev := posthogDrainDeadline
	posthogDrainDeadline = time.Second
	t.Cleanup(func() { posthogDrainDeadline = prev })

	cases := []struct {
		name       string
		status     int
		wantSent   int64
		wantStatus int64
	}{
		{"200 ok", http.StatusOK, 1, 0},
		{"204 ok", http.StatusNoContent, 1, 0},
		{"401 token revoked", http.StatusUnauthorized, 0, 1},
		{"413 too large", http.StatusRequestEntityTooLarge, 0, 1},
		{"429 rate limited", http.StatusTooManyRequests, 0, 1},
		{"500", http.StatusInternalServerError, 0, 1},
		{"503", http.StatusServiceUnavailable, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(c.status)
			}))
			defer ts.Close()

			sink := newPostHogSink(slog.LevelWarn, ts.URL, "tok", ts.Client(), time.Hour, Options{Name: "ogcode.log"})
			slog.New(&posthogHandler{sink: sink}).Warn("boom")
			sink.stop()
			st := sink.snapshot()
			if st.Sent != c.wantSent || st.DroppedStatus != c.wantStatus {
				t.Errorf("stats = %+v, want sent %d, droppedStatus %d", st, c.wantSent, c.wantStatus)
			}
			if st.DroppedQueue+st.DroppedEncode+st.DroppedRequest+st.DroppedTransport+st.Unflushed != 0 {
				t.Errorf("unexpected drops: %+v", st)
			}
		})
	}
}

// A batch that never reaches a server is still counted: an unbuildable
// request under DroppedRequest, a refused connection under DroppedTransport.
func TestSendCountsRequestAndTransportFailures(t *testing.T) {
	prev := posthogDrainDeadline
	posthogDrainDeadline = time.Second
	t.Cleanup(func() { posthogDrainDeadline = prev })

	// An endpoint http.NewRequest cannot even parse.
	sink := newPostHogSink(slog.LevelWarn, "://no-scheme", "tok", http.DefaultClient, time.Hour, Options{Name: "ogcode.log"})
	slog.New(&posthogHandler{sink: sink}).Warn("boom")
	sink.stop()
	if st := sink.snapshot(); st.DroppedRequest != 1 || st.Sent != 0 {
		t.Errorf("unbuildable request: stats = %+v, want droppedRequest 1", st)
	}

	// A server that was up when the sink was built and is gone now.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close()
	sink = newPostHogSink(slog.LevelWarn, url, "tok", http.DefaultClient, time.Hour, Options{Name: "ogcode.log"})
	slog.New(&posthogHandler{sink: sink}).Warn("boom")
	sink.stop()
	if st := sink.snapshot(); st.DroppedTransport != 1 || st.Sent != 0 {
		t.Errorf("refused connection: stats = %+v, want droppedTransport 1", st)
	}
}

// A full queue drops the record, counts it, and warns on stderr — rate
// limited to once a minute — with the cumulative drop count. The warning is
// written directly, never through slog, which would re-enter the sink.
func TestQueueFullCountsAndWarns(t *testing.T) {
	sink := &posthogSink{level: slog.LevelWarn, queue: make(chan posthogRecord, 2), done: make(chan struct{})}
	h := &posthogHandler{sink: sink}

	var buf bytes.Buffer
	prev := posthogWarnWriter
	posthogWarnWriter = &buf
	t.Cleanup(func() { posthogWarnWriter = prev })

	rec := func() {
		if err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)); err != nil {
			t.Fatal(err)
		}
	}

	rec()
	rec()
	rec() // drop one
	if got := sink.stats.droppedQueue.Load(); got != 1 {
		t.Errorf("droppedQueue = %d, want 1", got)
	}
	if n := strings.Count(buf.String(), "queue full"); n != 1 {
		t.Errorf("first drop should warn exactly once, got %d:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "ogcode: posthog log shipping") ||
		!strings.Contains(buf.String(), "1 records dropped so far") ||
		!strings.Contains(buf.String(), "queue size 2") {
		t.Errorf("warning lacks the sink name, drop count and queue size:\n%s", buf.String())
	}

	rec() // second drop inside the minute stays silent
	if got := sink.stats.droppedQueue.Load(); got != 2 {
		t.Errorf("droppedQueue = %d, want 2", got)
	}
	if n := strings.Count(buf.String(), "queue full"); n != 1 {
		t.Errorf("rate-limited warning fired again:\n%s", buf.String())
	}

	// A minute later it warns again, with the cumulative count.
	sink.lastQueueWarn.Store(time.Now().Add(-2 * posthogQueueWarnEvery).UnixNano())
	rec()
	if got := sink.stats.droppedQueue.Load(); got != 3 {
		t.Errorf("droppedQueue = %d, want 3", got)
	}
	if n := strings.Count(buf.String(), "queue full"); n != 2 {
		t.Errorf("expiry did not allow the next warning:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "3 records dropped so far") {
		t.Errorf("later warning lacks the cumulative count:\n%s", buf.String())
	}
}

// Records logged after Close are counted as dropped, not parked in a dead
// queue.
func TestHandleAfterStopCountsDrop(t *testing.T) {
	prev := posthogWarnWriter
	posthogWarnWriter = io.Discard
	t.Cleanup(func() { posthogWarnWriter = prev })

	sink := &posthogSink{level: slog.LevelWarn, queue: make(chan posthogRecord, 1), done: make(chan struct{})}
	sink.stop()
	if !sink.stopped.Load() {
		t.Fatal("stop did not set stopped")
	}
	slog.New(&posthogHandler{sink: sink}).Warn("after close")
	if got := sink.stats.droppedQueue.Load(); got != 1 {
		t.Errorf("droppedQueue = %d, want 1", got)
	}
	if len(sink.queue) != 0 {
		t.Errorf("a record entered the stopped queue (len %d)", len(sink.queue))
	}
}

// stop() bounds its wait: with the runner stuck on a send past the deadline,
// stop returns promptly and whatever is still queued counts as unflushed,
// rather than hanging process exit for the full drain.
func TestStopCountsUnflushedAtDeadline(t *testing.T) {
	prev := posthogDrainDeadline
	posthogDrainDeadline = 100 * time.Millisecond
	t.Cleanup(func() { posthogDrainDeadline = prev })

	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // the endpoint never answers within the test
	}))
	defer ts.Close()
	defer close(block) // releases the handler before ts.Close waits on connections

	sink := newPostHogSink(slog.LevelWarn, ts.URL, "tok", &http.Client{}, time.Hour, Options{Name: "ogcode.log"})
	for i := 0; i < 200; i++ {
		sink.queue <- posthogRecord{rec: slog.NewRecord(time.Now(), slog.LevelError, "stranded", 0)}
	}
	start := time.Now()
	sink.stop()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("stop waited %s; the drain deadline did not bound it", elapsed)
	}
	if got := sink.stats.unflushed.Load(); got == 0 {
		t.Error("records stuck behind a hung send should count as unflushed")
	}
}

// OGCODE_LOG_LEVEL=off means logging is off: no sink is attached, so warn+
// records ship nowhere. The endpoint is stubbed here so an accidental request
// could be noticed rather than reach a real network.
func TestOffFileLevelAttachesNoSink(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv(posthogLogsFlagEnv, "on")
	t.Setenv(posthogLogsLevelEnv, "")

	var mu sync.Mutex
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
	}))
	defer ts.Close()
	prev := posthogLogsEndpoint
	posthogLogsEndpoint = ts.URL
	t.Cleanup(func() { posthogLogsEndpoint = prev })

	if (Options{Name: "ogcode.log", Level: LevelOff}).shipsLogs() {
		t.Error("an off file level should not ship logs")
	}
	l := New(Options{Name: "ogcode.log", Dir: t.TempDir(), Level: LevelOff, Console: LevelOff, Stderr: io.Discard})
	defer l.Close()
	if st := l.ShipStats(); st.Enabled {
		t.Errorf("an off file level still attached the shipping sink: %+v", st)
	}
	l.Warn("shipped nowhere")
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Errorf("a disabled sink sent %d requests", requests)
	}
}

// Close logs one summary through the still-installed logger: Info on a quiet
// day, Warn when anything was dropped. This is what lets a support engineer
// tell "PostHog is dropping everything" from "a quiet day".
func TestCloseLogsShippingSummary(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv(posthogLogsFlagEnv, "on")
	t.Setenv(posthogLogsLevelEnv, "")
	prevDeadline := posthogDrainDeadline
	posthogDrainDeadline = 2 * time.Second
	t.Cleanup(func() { posthogDrainDeadline = prevDeadline })

	cases := []struct {
		name        string
		status      int
		wantConsole bool // a drop surfaces the summary at the warn console default
		wantStat    func(t *testing.T, st ShipStats)
	}{
		{"quiet day", http.StatusOK, false, func(t *testing.T, st ShipStats) {
			if st.Sent == 0 {
				t.Errorf("quiet-day summary on stats %+v should record sends", st)
			}
			if st.DroppedQueue+st.DroppedEncode+st.DroppedRequest+st.DroppedTransport+st.DroppedStatus+st.Unflushed != 0 {
				t.Errorf("quiet day had drops: %+v", st)
			}
		}},
		{"endpoint rejects", http.StatusInternalServerError, true, func(t *testing.T, st ShipStats) {
			if st.DroppedStatus != 1 {
				t.Errorf("droppedStatus = %d, want 1 (%+v)", st.DroppedStatus, st)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(c.status)
			}))
			defer ts.Close()
			prev := posthogLogsEndpoint
			posthogLogsEndpoint = ts.URL
			t.Cleanup(func() { posthogLogsEndpoint = prev })

			var console bytes.Buffer
			o := Options{Dir: t.TempDir(), Name: "ogcode.log", Level: slog.LevelInfo, Format: "text",
				Console: slog.LevelWarn, Rotate: RotateOptions{MaxSize: 1 << 20}, Stderr: &console}
			l := New(o)
			l.Warn("shipped record")
			l.Error("also shipped")
			l.Close()
			st := l.ShipStats()
			c.wantStat(t, st)

			file, err := os.ReadFile(filepath.Join(o.Dir, "ogcode.log"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(file), "posthog log shipping summary") {
				t.Errorf("file lacks the shipping summary:\n%s", file)
			}
			if c.wantConsole && !strings.Contains(console.String(), "posthog log shipping summary") {
				t.Errorf("dropped records should surface the summary at warn:\n%s", console.String())
			}
			if !c.wantConsole && strings.Contains(console.String(), "posthog log shipping summary") {
				t.Errorf("a quiet day should log the summary at info, below the console:\n%s", console.String())
			}
		})
	}
}
