package logging

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		{"", false}, // opt-in: unset and empty are off
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
	if (Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("shipping is opt-in; a named logger should not ship by default")
	}
	if (Options{}).shipsLogs() {
		t.Error("a logger without a file should not ship logs")
	}
	t.Setenv(posthogLogsFlagEnv, "1")
	if !(Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("OGCODE_POSTHOG_LOGS=1 should ship logs")
	}
	t.Setenv(posthogLogsFlagEnv, "off")
	if (Options{Name: "ogcode.log"}).shipsLogs() {
		t.Error("OGCODE_POSTHOG_LOGS=off should stop shipping")
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
	sink := newPostHogSink(slog.LevelWarn, ts.URL, "tok", ts.Client(), time.Hour)
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
