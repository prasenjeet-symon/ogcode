package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

// fakeIncus is a hand-rolled REST fake speaking the Incus /1.0 wire protocol
// over a unix socket — just enough for the real driver: server handshake,
// instance list/create/state/delete, operation wait. Operations complete
// synchronously (the fake applies the change and reports Success).
type fakeIncus struct {
	mu         sync.Mutex
	instances  map[string]*api.Instance
	created    []api.InstancesPost
	actions    []string // recorded instance-state actions, in order
	opSeq      int
	nextOpFail string // when set, the next async op finishes with this error
}

func newFakeIncus(t *testing.T) (*fakeIncus, string) {
	t.Helper()
	f := &fakeIncus{instances: map[string]*api.Instance{}}
	// macOS caps unix socket paths at 104 bytes; t.TempDir() paths overflow
	// it, so bind a short path under the system temp root and remove it at
	// cleanup.
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("ogincus-%d.sock", os.Getpid()))
	_ = os.Remove(sock) // stale socket from an aborted run would fail bind
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/1.0", f.serve)
	mux.HandleFunc("/1.0/", f.serve)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f, sock
}

func (f *fakeIncus) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rest := strings.TrimPrefix(r.URL.Path, "/1.0")
	if r.URL.Path == "/1.0" || r.URL.Path == "/1.0/" {
		writeJSON(w, http.StatusOK, envelope("sync", api.Server{
			ServerUntrusted: api.ServerUntrusted{
				APIExtensions: []string{},
				APIStatus:     "stable",
				APIVersion:    "1.0",
				Auth:          "trusted",
				ServerPut:     api.ServerPut{Config: api.ConfigMap{}},
			},
			Environment: api.ServerEnvironment{},
		}))
		return
	}

	switch {
	case rest == "/instances" && r.Method == http.MethodGet:
		list := []api.Instance{}
		for _, inst := range f.instances {
			list = append(list, *inst)
		}
		writeJSON(w, http.StatusOK, envelope("sync", list))
		return

	case rest == "/instances" && r.Method == http.MethodPost:
		var req api.InstancesPost
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad request: %v", err)
			return
		}
		if _, exists := f.instances[req.Name]; exists {
			writeError(w, http.StatusConflict, "instance %q already exists", req.Name)
			return
		}
		if f.nextOpFail != "" {
			f.nextOpFail = ""
			writeError(w, http.StatusInternalServerError, "create failed by request")
			return
		}
		f.created = append(f.created, req)
		f.instances[req.Name] = &api.Instance{
			Name: req.Name,
			InstancePut: api.InstancePut{
				Profiles: req.Profiles,
				Config:   req.Config,
			},
			StatusCode: api.Stopped,
			Status:     api.Stopped.String(),
		}
		f.writeAsyncOp(w)
		return

	case strings.HasPrefix(rest, "/instances/") && strings.HasSuffix(rest, "/state") && r.Method == http.MethodGet:
		name := strings.TrimSuffix(strings.TrimPrefix(rest, "/instances/"), "/state")
		inst, ok := f.instances[name]
		if !ok {
			writeError(w, http.StatusNotFound, "instance %q not found", name)
			return
		}
		writeJSON(w, http.StatusOK, envelope("sync", api.InstanceState{
			StatusCode: inst.StatusCode,
			Status:     inst.Status,
		}))
		return

	case strings.HasPrefix(rest, "/instances/") && strings.HasSuffix(rest, "/state") && r.Method == http.MethodPut:
		name := strings.TrimSuffix(strings.TrimPrefix(rest, "/instances/"), "/state")
		inst, ok := f.instances[name]
		if !ok {
			writeError(w, http.StatusNotFound, "instance %q not found", name)
			return
		}
		var put api.InstanceStatePut
		if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
			writeError(w, http.StatusBadRequest, "bad request: %v", err)
			return
		}
		f.actions = append(f.actions, put.Action)
		switch put.Action {
		case "start":
			inst.StatusCode = api.Running
			inst.Status = api.Running.String()
		case "stop":
			inst.StatusCode = api.Stopped
			inst.Status = api.Stopped.String()
		}
		f.writeAsyncOp(w)
		return

	case strings.HasPrefix(rest, "/instances/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(rest, "/instances/")
		if _, ok := f.instances[name]; !ok {
			writeError(w, http.StatusNotFound, "instance %q not found", name)
			return
		}
		delete(f.instances, name)
		f.writeAsyncOp(w)
		return

	case strings.HasPrefix(rest, "/operations/") && strings.HasSuffix(rest, "/wait") && r.Method == http.MethodGet:
		// The fake applies changes synchronously, so every op is already in
		// its final state by the time the client polls.
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/operations/"), "/wait")
		writeJSON(w, http.StatusOK, envelope("sync", api.Operation{
			ID:         id,
			Status:     api.Success.String(),
			StatusCode: api.Success,
			Err:        "",
		}))
		return

	default:
		writeError(w, http.StatusNotFound, "fake: unhandled %s %s", r.Method, r.URL.Path)
		return
	}
}

// writeAsyncOp returns the 202 async envelope the client's queryOperation
// parses; the matching /operations/<id>/wait call below always reports the
// op finished successfully.
func (f *fakeIncus) writeAsyncOp(w http.ResponseWriter) {
	f.opSeq++
	id := fmt.Sprintf("op-%d", f.opSeq)
	op := api.Operation{
		ID:         id,
		Status:     api.Success.String(),
		StatusCode: api.Success,
	}
	writeJSON(w, http.StatusAccepted, envelope("async", op))
}

func envelope(kind string, metadata any) map[string]any {
	status, code := "Success", http.StatusOK
	if kind == "async" {
		status, code = "OK", http.StatusAccepted
	}
	return map[string]any{"type": kind, "status": status, "status_code": code, "metadata": metadata}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]any{
		"type":       "error",
		"error":      fmt.Sprintf(format, args...),
		"error_code": code,
	})
}

func testDriver(t *testing.T, sock string) Driver {
	t.Helper()
	d, err := NewDriver(Config{Socket: sock, Profile: "ogcode-worker", ImageAlias: "ogcode-base"})
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	return d
}

func TestDriver_CreateStartDeleteState(t *testing.T) {
	f, sock := newFakeIncus(t)
	d := testDriver(t, sock)
	ctx := context.Background()

	err := d.Create(ctx, "og-api-alice", Seed{
		MasterURL:     "https://panel.example.com",
		PairingSecret: "sekrit",
		RepoURL:       "https://github.com/org/api.git",
		RepoSlug:      "github.com-org-api",
		WorkerID:      "og-api-alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	f.mu.Lock()
	if len(f.created) != 1 {
		t.Fatalf("created %d instances, want 1", len(f.created))
	}
	req := f.created[0]
	f.mu.Unlock()

	if req.Name != "og-api-alice" || req.Type != api.InstanceTypeContainer {
		t.Errorf("name/type = %q/%q", req.Name, req.Type)
	}
	if len(req.Profiles) != 1 || req.Profiles[0] != "ogcode-worker" {
		t.Errorf("profiles = %v, want [ogcode-worker]", req.Profiles)
	}
	if req.Source.Type != "image" || req.Source.Alias != "ogcode-base" {
		t.Errorf("source = %+v, want image/ogcode-base", req.Source)
	}
	if got := req.Config["user.ogcode.worker-id"]; got != "og-api-alice" {
		t.Errorf("user.ogcode.worker-id = %q", got)
	}
	if got := req.Config["user.ogcode.master-url"]; got != "https://panel.example.com" {
		t.Errorf("user.ogcode.master-url = %q", got)
	}
	ud := req.Config["cloud-init.user-data"]
	if !strings.HasPrefix(ud, "#cloud-config\n") {
		t.Errorf("cloud-init.user-data missing #cloud-config header: %q", ud[:40])
	}

	f.mu.Lock()
	acts := append([]string(nil), f.actions...)
	f.mu.Unlock()
	if len(acts) != 1 || acts[0] != "start" {
		t.Errorf("state actions = %v, want [start]", acts)
	}

	state, err := d.State(ctx, "og-api-alice")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state != StateRunning {
		t.Errorf("State = %q, want running", state)
	}

	if err := d.Delete(ctx, "og-api-alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	state, err = d.State(ctx, "og-api-alice")
	if err != nil {
		t.Fatalf("State after delete: %v", err)
	}
	if state != StateMissing {
		t.Errorf("State after delete = %q, want missing", state)
	}
}

func TestDriver_DeleteMissingIsIdempotent(t *testing.T) {
	_, sock := newFakeIncus(t)
	d := testDriver(t, sock)
	if err := d.Delete(context.Background(), "og-never-existed"); err != nil {
		t.Fatalf("Delete of missing container: %v (want nil)", err)
	}
}

func TestDriver_StateStopped(t *testing.T) {
	f, sock := newFakeIncus(t)
	d := testDriver(t, sock)
	ctx := context.Background()

	if err := d.Create(ctx, "og-api-alice", Seed{WorkerID: "og-api-alice"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Manually stop via a second driver call path: Delete stops running
	// containers first, so instead poke the fake directly.
	f.mu.Lock()
	f.instances["og-api-alice"].StatusCode = api.Stopped
	f.instances["og-api-alice"].Status = api.Stopped.String()
	f.mu.Unlock()

	state, err := d.State(ctx, "og-api-alice")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state != StateStopped {
		t.Errorf("State = %q, want stopped", state)
	}
}

func TestDriver_CreateFailureSurfaces(t *testing.T) {
	f, sock := newFakeIncus(t)
	d := testDriver(t, sock)
	f.mu.Lock()
	f.nextOpFail = "create failed by request"
	f.mu.Unlock()

	err := d.Create(context.Background(), "og-api-alice", Seed{WorkerID: "og-api-alice"})
	if err == nil {
		t.Fatal("Create succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "incus: create") {
		t.Errorf("error = %v, want it to mention incus create", err)
	}
}

func TestDriver_DeleteRunningStopsFirst(t *testing.T) {
	f, sock := newFakeIncus(t)
	d := testDriver(t, sock)
	ctx := context.Background()

	if err := d.Create(ctx, "og-api-alice", Seed{WorkerID: "og-api-alice"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := d.Delete(ctx, "og-api-alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.mu.Lock()
	acts := append([]string(nil), f.actions...)
	f.mu.Unlock()
	if len(acts) != 2 || acts[0] != "start" || acts[1] != "stop" {
		t.Errorf("state actions = %v, want [start stop]", acts)
	}
}

func TestDriver_ListAssignmentsFiltersPrefixAndCarriesLabels(t *testing.T) {
	f, sock := newFakeIncus(t)
	d := testDriver(t, sock)

	f.mu.Lock()
	f.instances["og-api-alice"] = &api.Instance{
		Name: "og-api-alice",
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"user.ogcode.worker-id": "og-api-alice",
				"user.ogcode.repo-url":  "https://github.com/org/api.git",
				"security.devlxd":       "false",
			},
		},
	}
	f.instances["other-x"] = &api.Instance{Name: "other-x"}
	f.mu.Unlock()

	got, err := d.ListAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d assignments, want 1 (only og-*): %+v", len(got), got)
	}
	a := got[0]
	if a.Name != "og-api-alice" || a.User != "og-api-alice" {
		t.Errorf("assignment = %+v", a)
	}
	if a.Labels["repo-url"] != "https://github.com/org/api.git" {
		t.Errorf("labels = %v", a.Labels)
	}
}

func TestCloudConfigYAML_Parity(t *testing.T) {
	seed := Seed{
		MasterURL:     "https://panel.example.com",
		PairingSecret: "sek",
		RepoURL:       "https://github.com/org/api.git",
		RepoSlug:      "github.com-org-api",
		WorkerID:      "og-api-alice",
		BaseBranch:    "main",
	}
	got := cloudConfigYAML(seed)
	for _, want := range []string{
		"#cloud-config",
		"- path: /etc/ogcode/master-url",
		"- path: /etc/ogcode/pairing-secret",
		"permissions: '0600'",
		"- path: /etc/ogcode/repo-url",
		"- path: /etc/ogcode/repo-slug",
		"- path: /root/.ogcode/worker-id",
		"- path: /etc/ogcode/worker.env",
		"OGCODE_MASTER_URL=https://panel.example.com",
		"OGCODE_WORKER_NAME=og-api-alice",
		"OGCODE_EXTRA_FLAGS=",
		"- path: /etc/ogcode/base-branch",
		"runcmd:",
		"- [systemctl, enable, --now, ogcode-clone.service]",
		"- [systemctl, enable, --now, ogcode-worker.service]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cloud-config missing %q\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "master-ca") {
		t.Errorf("cloud-config mentions master-ca with no CA set:\n%s", got)
	}

	withCA := cloudConfigYAML(Seed{WorkerID: "og-x", MasterCA: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n"})
	for _, want := range []string{
		"- path: /etc/ogcode/master-ca.pem",
		"OGCODE_EXTRA_FLAGS=--master-ca /etc/ogcode/master-ca.pem",
		"-----BEGIN CERTIFICATE-----",
	} {
		if !strings.Contains(withCA, want) {
			t.Errorf("cloud-config with CA missing %q\ngot:\n%s", want, withCA)
		}
	}
}

func TestNewDriver_ConnectFailure(t *testing.T) {
	dir := t.TempDir()
	_, err := NewDriver(Config{Socket: filepath.Join(dir, "absent.sock")})
	if err == nil {
		t.Fatal("connect to nonexistent socket succeeded")
	}
	if !strings.Contains(err.Error(), "incus: connect") {
		t.Errorf("error = %v, want connect wrapper", err)
	}
}
