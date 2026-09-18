// Package incus drives Incus (LXD fork) containers on the master's host.
//
// Container mode (INCUS_WORKERS_PLAN.md §Phase B): one container per
// user-repo assignment. The driver is deliberately narrow — create with
// cloud-init seed, delete, read state, list og-* containers — and it never
// shells out to `incus exec`; in-guest work happens through cloud-init
// user-data baked at create time (mirroring scripts/incus/assign.sh) and the
// worker's own Register call.
package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// InstanceState is the small slice of container state the master needs.
type InstanceState string

const (
	// StateRunning means the container exists and is running.
	StateRunning InstanceState = "running"
	// StateStopped means the container exists but is not running.
	StateStopped InstanceState = "stopped"
	// StateMissing means no such container.
	StateMissing InstanceState = "missing"
)

// Assignment is one og-* container as reported by ListAssignments.
type Assignment struct {
	Name   string
	User   string            // user.ogcode.worker-id mirror, empty when absent
	Labels map[string]string // user.ogcode.* operator metadata keys
}

// Seed is the cloud-init user-data payload for Create.
type Seed struct {
	MasterURL     string
	PairingSecret string
	RepoURL       string
	RepoSlug      string
	WorkerID      string // == container name
	BaseBranch    string
	MasterCA      string // PEM bundle for self-signed master TLS; empty = none
}

// Driver is the narrow surface the master needs; tests fake it.
type Driver interface {
	// Create makes and starts a container named name, applying seed as
	// cloud-init user-data. It waits for the create+start operations to
	// finish before returning.
	Create(ctx context.Context, name string, seed Seed) error
	// Delete removes the container (any state). It is not an error if the
	// container is already gone.
	Delete(ctx context.Context, name string) error
	// State reports whether the container is running, stopped, or missing.
	State(ctx context.Context, name string) (InstanceState, error)
	// ListAssignments lists this host's og-* containers. Orphan
	// reconciliation (Phase C) is its intended consumer.
	ListAssignments(ctx context.Context) ([]Assignment, error)
}

// Real driver over the Incus unix socket.

// Config configures the real driver.
type Config struct {
	// Socket is the unix socket path; empty means the Incus default socket.
	Socket string
	// Profile is the Incus profile applied to created containers.
	Profile string
	// ImageAlias is the image containers are created from.
	ImageAlias string
	// Timeout bounds each Incus REST call (not the cloud-init boot).
	Timeout time.Duration
}

// NewDriver connects to the Incus socket and returns a working Driver.
func NewDriver(cfg Config) (Driver, error) {
	if cfg.Profile == "" {
		cfg.Profile = "ogcode-worker"
	}
	if cfg.ImageAlias == "" {
		cfg.ImageAlias = "ogcode-base"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	args := &incus.ConnectionArgs{
		// The default event-listener path needs a websocket; polling waits
		// work fine for container lifecycle ops and keep the client simple.
		SkipGetEvents: true,
	}
	srv, err := incus.ConnectIncusUnix(cfg.Socket, args)
	if err != nil {
		return nil, fmt.Errorf("incus: connect %q: %w", socketDisplay(cfg.Socket), err)
	}
	return &realDriver{srv: srv, cfg: cfg}, nil
}

func socketDisplay(socket string) string {
	if socket == "" {
		return "default incus socket"
	}
	return socket
}

type realDriver struct {
	srv incus.InstanceServer
	cfg Config
}

// containerConfig assembles the instance config: cloud-init user-data plus the
// user.ogcode.* operator metadata (assign.sh parity — guest reads only the
// cloud-init files; devlxd is off).
func (d *realDriver) containerConfig(name string, seed Seed) map[string]string {
	cfg := map[string]string{
		"cloud-init.user-data":    cloudConfigYAML(seed),
		"user.ogcode.worker-id":   name,
		"user.ogcode.master-url":  seed.MasterURL,
		"user.ogcode.repo-url":    seed.RepoURL,
		"user.ogcode.base-branch": seed.BaseBranch,
	}
	if seed.MasterCA != "" {
		cfg["user.ogcode.master-ca"] = seed.MasterCA
	}
	return cfg
}

func (d *realDriver) Create(ctx context.Context, name string, seed Seed) error {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	req := api.InstancesPost{
		Name: name,
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: d.cfg.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Profiles: []string{d.cfg.Profile},
			Config:   d.containerConfig(name, seed),
		},
	}
	op, err := d.srv.CreateInstance(req)
	if err != nil {
		return fmt.Errorf("incus: create %s: %w", name, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return fmt.Errorf("incus: create %s: %w", name, err)
	}

	state := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}
	op, err = d.srv.UpdateInstanceState(name, state, "")
	if err != nil {
		return fmt.Errorf("incus: start %s: %w", name, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return fmt.Errorf("incus: start %s: %w", name, err)
	}
	return nil
}

func (d *realDriver) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	// Best-effort stop first: Incus refuses to delete a running container.
	// A stopped-but-present container is still deleted below.
	state, _, err := d.srv.GetInstanceState(name)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("incus: state %s: %w", name, err)
	}
	if err == nil && state.StatusCode == api.Running {
		op, err := d.srv.UpdateInstanceState(name, api.InstanceStatePut{
			Action:  "stop",
			Timeout: -1,
			Force:   true,
		}, "")
		if err != nil {
			return fmt.Errorf("incus: stop %s: %w", name, err)
		}
		if err := op.WaitContext(ctx); err != nil {
			return fmt.Errorf("incus: stop %s: %w", name, err)
		}
	}

	op, err := d.srv.DeleteInstance(name)
	if err != nil {
		if isNotFound(err) {
			return nil // already gone: delete is idempotent
		}
		return fmt.Errorf("incus: delete %s: %w", name, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("incus: delete %s: %w", name, err)
	}
	return nil
}

func (d *realDriver) State(ctx context.Context, name string) (InstanceState, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	state, _, err := d.srv.GetInstanceState(name)
	if err != nil {
		if isNotFound(err) {
			return StateMissing, nil
		}
		return "", fmt.Errorf("incus: state %s: %w", name, err)
	}
	if state.StatusCode == api.Running {
		return StateRunning, nil
	}
	return StateStopped, nil
}

func (d *realDriver) ListAssignments(ctx context.Context) ([]Assignment, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	prefix := "og-"
	instances, err := d.srv.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("incus: list: %w", err)
	}
	var out []Assignment
	for _, inst := range instances {
		if !strings.HasPrefix(inst.Name, prefix) {
			continue
		}
		a := Assignment{
			Name:   inst.Name,
			Labels: map[string]string{},
		}
		for k, v := range inst.Config {
			if strings.HasPrefix(k, "user.ogcode.") {
				a.Labels[strings.TrimPrefix(k, "user.ogcode.")] = v
			}
		}
		a.User = a.Labels["worker-id"]
		out = append(out, a)
	}
	return out, nil
}

// isNotFound reports whether err is Incus's 404 shape.
func isNotFound(err error) bool {
	return api.StatusErrorCheck(err, 404)
}

// cloudConfigYAML renders the cloud-init user-data. Byte-parity with the
// assign.sh heredoc (scripts/incus/assign.sh) — the guest systemd units read
// exactly these paths.
func cloudConfigYAML(seed Seed) string {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	b.WriteString("write_files:\n")
	writeFile := func(path, perm, content string) {
		fmt.Fprintf(&b, "  - path: %s\n", path)
		fmt.Fprintf(&b, "    permissions: '%s'\n", perm)
		b.WriteString("    content: |\n")
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			fmt.Fprintf(&b, "      %s\n", line)
		}
	}
	writeFile("/etc/ogcode/master-url", "0644", seed.MasterURL)
	writeFile("/etc/ogcode/pairing-secret", "0600", seed.PairingSecret)
	writeFile("/etc/ogcode/repo-url", "0644", seed.RepoURL)
	writeFile("/etc/ogcode/repo-slug", "0644", seed.RepoSlug)
	writeFile("/etc/ogcode/base-branch", "0644", seed.BaseBranch)
	writeFile("/root/.ogcode/worker-id", "0644", seed.WorkerID)
	var env strings.Builder
	fmt.Fprintf(&env, "OGCODE_MASTER_URL=%s\n", seed.MasterURL)
	fmt.Fprintf(&env, "OGCODE_WORKER_NAME=%s\n", seed.WorkerID)
	if seed.MasterCA != "" {
		fmt.Fprintf(&env, "OGCODE_EXTRA_FLAGS=--master-ca /etc/ogcode/master-ca.pem\n")
	} else {
		fmt.Fprint(&env, "OGCODE_EXTRA_FLAGS=\n")
	}
	writeFile("/etc/ogcode/worker.env", "0644", strings.TrimSuffix(env.String(), "\n"))
	if seed.MasterCA != "" {
		fmt.Fprintf(&b, "  - path: /etc/ogcode/master-ca.pem\n")
		fmt.Fprintf(&b, "    permissions: '0644'\n")
		b.WriteString("    content: |\n")
		for _, line := range strings.Split(strings.TrimSuffix(seed.MasterCA, "\n"), "\n") {
			fmt.Fprintf(&b, "      %s\n", line)
		}
	}
	b.WriteString("runcmd:\n")
	b.WriteString("  - [systemctl, enable, --now, ogcode-clone.service]\n")
	b.WriteString("  - [systemctl, enable, --now, ogcode-worker.service]\n")
	return b.String()
}
