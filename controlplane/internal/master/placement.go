package master

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/incus"
)

// Container-mode assignment (INCUS_WORKERS_PLAN.md §Phase B): one Incus
// container per user-repo assignment. The container registers as an ordinary
// worker (cloud-init writes the container name into /root/.ogcode/worker-id),
// so nothing downstream — tunnels, panel subdomains, sessions — distinguishes
// a container worker from a bare one. This file owns everything the master
// adds on top: capacity, provisioning, readiness, removal, and the name math.

// ErrCapacity is returned when the container cap is reached.
var ErrCapacity = errors.New("container cap reached — deprovision or destroy an assignment first")

// Placement statuses.
const (
	// StatusProvisioning marks an assignment whose container exists (or is
	// still being created) but whose worker has not registered yet.
	StatusProvisioning = "provisioning"
	// StatusReady marks a placement whose worker has registered.
	StatusReady = "ready"
	// StatusFailed marks a placement whose provisioning failed (create error
	// or the register timeout passed).
	StatusFailed = "failed"
)

// Placement is one user-repo assignment in container mode. The key is
// (User, RepoURL) — global uniqueness, because the container name folds both
// and every container registers with this one master.
type Placement struct {
	User          string    `json:"user"`
	RepoURL       string    `json:"repoUrl"`
	RepoSlug      string    `json:"repoSlug"`
	ContainerName string    `json:"containerName"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
}

// placementStore persists placements in the registry bbolt file's
// "placements" bucket (created by registry.Open), keyed by
// "<user>\x00<repoURL>". bbolt serializes writers, so no extra lock.
type placementStore struct {
	db *bolt.DB
}

// ErrNoPlacement is returned when no placement matches the key. Distinct from
// ErrNoWorkerOnline (bare mode) so callers can tell the two placement worlds
// apart.
var ErrNoPlacement = errors.New("no container placement for this user and repo")

func newPlacementStore(db *bolt.DB) *placementStore {
	return &placementStore{db: db}
}

func placementKey(user, repoURL string) []byte {
	return []byte(user + "\x00" + repoURL)
}

// put inserts or replaces a placement.
func (ps *placementStore) put(p Placement) error {
	v, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode placement: %w", err)
	}
	return ps.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("placements")).Put(placementKey(p.User, p.RepoURL), v)
	})
}

// get returns a copy of the placement at the key.
func (ps *placementStore) get(user, repoURL string) (Placement, bool, error) {
	var out Placement
	found := false
	err := ps.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte("placements")).Get(placementKey(user, repoURL))
		if v == nil {
			return nil
		}
		found = true
		return json.Unmarshal(v, &out)
	})
	if err != nil {
		return Placement{}, false, err
	}
	return out, found, nil
}

// forget drops the key. Removing an absent key is not an error.
func (ps *placementStore) forget(user, repoURL string) error {
	return ps.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("placements")).Delete(placementKey(user, repoURL))
	})
}

// list returns snapshot copies sorted by user then repo URL.
func (ps *placementStore) list() ([]Placement, error) {
	var out []Placement
	err := ps.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("placements")).ForEach(func(_, v []byte) error {
			var p Placement
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			out = append(out, p)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User < out[j].User
		}
		return out[i].RepoURL < out[j].RepoURL
	})
	return out, nil
}

// byRepo returns every placement of one repo URL (snapshot).
func (ps *placementStore) byRepo(repoURL string) ([]Placement, error) {
	all, err := ps.list()
	if err != nil {
		return nil, err
	}
	var out []Placement
	for _, p := range all {
		if p.RepoURL == repoURL {
			out = append(out, p)
		}
	}
	return out, nil
}

// count counts non-failed placements — the live container population the
// capacity cap is measured against (a failed placement freed its name).
func (ps *placementStore) count() (int, error) {
	all, err := ps.list()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range all {
		if p.Status != StatusFailed {
			n++
		}
	}
	return n, nil
}

// completePending marks the provisioning placement whose container name is
// workerID as ready. A worker id that matches no pending placement completes
// nothing — unexpected registrations are admitted to the registry untouched
// (an operator-run assign.sh container presents an og-* id too).
func (ps *placementStore) completePending(workerID string) (Placement, error) {
	var hit Placement
	err := ps.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("placements"))
		type keyHit struct{ user, repo string }
		var found []keyHit
		err := b.ForEach(func(k, v []byte) error {
			var p Placement
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			if p.Status == StatusProvisioning && p.ContainerName == workerID {
				found = append(found, keyHit{user: p.User, repo: p.RepoURL})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return nil
		}
		// Container names are unique per master; more than one hit would mean
		// a duplicate (user, repo) key, which put() already collapses.
		v := b.Get(placementKey(found[0].user, found[0].repo))
		var p Placement
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		p.Status = StatusReady
		hit = p
		v2, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return b.Put(placementKey(p.User, p.RepoURL), v2)
	})
	if err != nil {
		return Placement{}, err
	}
	return hit, nil
}

// reapProvisioning fails every placement stuck in provisioning past the
// deadline, returning the placements it failed (the reaper logs them).
func (ps *placementStore) reapProvisioning(timeout time.Duration, now time.Time) ([]Placement, error) {
	var reaped []Placement
	err := ps.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("placements"))
		type keyHit struct{ user, repo string }
		var found []keyHit
		err := b.ForEach(func(k, v []byte) error {
			var p Placement
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			if p.Status == StatusProvisioning && now.Sub(p.CreatedAt) > timeout {
				found = append(found, keyHit{user: p.User, repo: p.RepoURL})
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range found {
			v := b.Get(placementKey(k.user, k.repo))
			var p Placement
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			p.Status = StatusFailed
			v2, err := json.Marshal(p)
			if err != nil {
				return err
			}
			if err := b.Put(placementKey(p.User, p.RepoURL), v2); err != nil {
				return err
			}
			reaped = append(reaped, p)
		}
		return nil
	})
	return reaped, err
}

// ContainerName exposes the name math for tests and the panel (the mirror of
// scripts/incus/assign.sh's budget arithmetic).
func ContainerName(namePrefix, repoURL, userName string) string {
	return containerName(namePrefix, repoURL, userName)
}

// ContainerMode reports whether this server provisions containers.
func (s *Server) ContainerMode() bool { return s.incus != nil }

// assignUserContainer provisions the user-repo container and applies the
// account scoping. Idempotent: re-assigning a live placement is a no-op that
// returns the container name; a placement whose container is gone
// (operator deleted it out-of-band) is re-provisioned under the same name.
// Readiness is NOT awaited here — the create+start operations finish, but the
// worker's Register is the readiness signal (status turns ready in Register).
func (s *Server) assignUserContainer(ctx context.Context, repoURL, userName, baseBranch string) (string, error) {
	store := s.reg.Store()

	// Idempotent re-assign: a live placement (ready, or still provisioning —
	// Create runs synchronously below, so a provisioning placement here means
	// a prior attempt whose worker has not registered yet) answers with its
	// container name.
	if existing, ok, err := s.placements.get(userName, repoURL); err != nil {
		return "", fmt.Errorf("read placement: %w", err)
	} else if ok && existing.Status != StatusFailed {
		state, err := s.incus.Driver.State(ctx, existing.ContainerName)
		if err != nil {
			return "", err
		}
		if state != incus.StateMissing {
			if err := s.scopeAccount(store, userName, repoURL, baseBranch); err != nil {
				return "", err
			}
			return existing.ContainerName, nil
		}
		// Container vanished out-of-band: re-provision under the same name.
	}

	// Capacity: live containers (provisioning + ready) count against the cap;
	// a failed placement freed its name.
	n, err := s.placements.count()
	if err != nil {
		return "", fmt.Errorf("count placements: %w", err)
	}
	if n >= s.incus.MaxContainers {
		return "", fmt.Errorf("%w: %d/%d containers", ErrCapacity, n, s.incus.MaxContainers)
	}

	name := containerName(s.incus.NamePrefix, repoURL, userName)
	if !validWorkerID(name) {
		return "", fmt.Errorf("derived container name %q is not a valid worker id", name)
	}
	// A failed placement being retried keeps its name (same user+repo folds
	// identically) so the cloud-init seed and panel label stay stable.
	if existing, ok, err := s.placements.get(userName, repoURL); err != nil {
		return "", err
	} else if ok {
		name = existing.ContainerName
	}

	seed := s.placementSeed(name, repoURL, baseBranch)
	p := Placement{
		User:          userName,
		RepoURL:       repoURL,
		RepoSlug:      seed.RepoSlug,
		ContainerName: name,
		Status:        StatusProvisioning,
		CreatedAt:     time.Now(),
	}
	if err := s.placements.put(p); err != nil {
		return "", fmt.Errorf("record placement: %w", err)
	}

	if err := s.incus.Driver.Create(ctx, name, seed); err != nil {
		p.Status = StatusFailed
		if putErr := s.placements.put(p); putErr != nil {
			s.logger.Error("mark placement failed", "container", name, "err", putErr)
		}
		return "", err
	}
	s.logger.Info("container created; awaiting worker registration", "container", name,
		"user", userName, "repo", seed.RepoSlug)

	if err := s.scopeAccount(store, userName, repoURL, baseBranch); err != nil {
		return "", err
	}
	return name, nil
}

// removeContainerAssignment drops one user-repo container assignment: delete
// the container, forget the placement, drop the repo from the account, and
// unroute the user's live sessions off the container's worker id. The user's
// branch user/<name> survives only if the in-guest unit pushed it — best
// effort by design (the plan's known unpushed-branch risk).
func (s *Server) removeContainerAssignment(ctx context.Context, repoURL, userName string) error {
	p, ok, err := s.placements.get(userName, repoURL)
	if err != nil {
		return fmt.Errorf("read placement: %w", err)
	}
	if !ok {
		return ErrNoPlacement
	}
	if err := s.incus.Driver.Delete(ctx, p.ContainerName); err != nil {
		return err
	}
	if err := s.placements.forget(userName, repoURL); err != nil {
		return fmt.Errorf("forget placement: %w", err)
	}

	// Mirror the bare path's bookkeeping: the account loses the repo; live
	// sessions the user holds against this container are unrouted.
	store := s.reg.Store()
	if store != nil {
		if uRec, found, err := store.GetUser(userName); err == nil && found {
			if next, removed := uRec.WithRepoRemoved(repoURL); removed {
				if err := store.PutUser(userName, next); err != nil {
					s.logger.Error("container unassign: drop repo from account", "user", userName, "repo", p.RepoSlug, "err", err)
				}
			}
		}
	}
	s.unrouteUserSessions(p.ContainerName, map[string]struct{}{userName: {}})

	s.repos.forget(p.RepoSlug)
	s.logger.Info("container assignment removed", "container", p.ContainerName,
		"user", userName, "repo", p.RepoSlug)
	return nil
}

// deprovisionContainerRepo destroys every container holding one repo and
// forgets the placements, applying the same account + routing bookkeeping the
// bare path performs. It returns the destroyed container names.
func (s *Server) deprovisionContainerRepo(ctx context.Context, repoURL string) ([]string, error) {
	placements, err := s.placements.byRepo(repoURL)
	if err != nil {
		return nil, fmt.Errorf("list placements: %w", err)
	}
	if len(placements) == 0 {
		return nil, fmt.Errorf("repo %s has no placement — assign a user first", repoURL)
	}
	destroyed := make([]string, 0, len(placements))
	deassigned := map[string]struct{}{}
	for _, p := range placements {
		if err := s.incus.Driver.Delete(ctx, p.ContainerName); err != nil {
			return destroyed, fmt.Errorf("delete %s: %w", p.ContainerName, err)
		}
		if err := s.placements.forget(p.User, p.RepoURL); err != nil {
			return destroyed, fmt.Errorf("forget placement %s/%s: %w", p.User, p.RepoSlug, err)
		}
		deassigned[p.User] = struct{}{}
		destroyed = append(destroyed, p.ContainerName)
	}

	store := s.reg.Store()
	if store != nil {
		if names, err := store.ListUsers(); err == nil {
			for _, name := range names {
				if _, hit := deassigned[name]; !hit {
					continue
				}
				uRec, found, err := store.GetUser(name)
				if err != nil || !found {
					continue
				}
				next, removed := uRec.WithRepoRemoved(repoURL)
				if !removed {
					continue
				}
				if err := store.PutUser(name, next); err != nil {
					s.logger.Error("container deprovision: drop repo from account", "user", name, "repo", repoSlugFromURL(repoURL), "err", err)
				}
			}
		}
	}
	for _, name := range destroyed {
		s.unrouteUserSessions(name, deassigned)
	}
	s.logger.Info("repo containers destroyed", "repo", repoSlugFromURL(repoURL), "containers", destroyed)
	return destroyed, nil
}

// RemoveUserWorktree drops one user's worktree from its repo's placement,
// keeping the user's branch (the work survives on user/<slug> and can be
// merged later). Bare mode Calls the placement worker; container mode deletes
// the user's container. The repo must already have a placement — assignment
// is what teaches the master where a repo lives.
func (s *Server) RemoveUserWorktree(ctx context.Context, repoURL, userName string) error {
	repoURL = strings.TrimSpace(repoURL)
	userName = strings.TrimSpace(userName)
	if repoURL == "" {
		return errors.New("repo url required")
	}
	if userName == "" {
		return errors.New("user name required")
	}
	if s.incus != nil {
		return s.removeContainerAssignment(ctx, repoURL, userName)
	}
	rec, ok := s.repos.get(repoSlugFromURL(repoURL))
	if !ok {
		return fmt.Errorf("repo %s has no placement — assign a user first", repoURL)
	}
	res, err := s.Call(ctx, rec.WorkerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_RemoveUserWorktree{RemoveUserWorktree: &cpv1.RemoveUserWorktree{
			RepoUrl:    rec.URL,
			UserName:   userName,
			KeepBranch: true,
		}},
	})
	if err != nil {
		return err
	}
	if !res.GetOk() {
		return fmt.Errorf("worker %q could not remove %q from %s: %s", rec.WorkerID, userName, rec.URL, res.GetError())
	}
	s.logger.Info("user worktree removed", "user", userName, "repo", repoSlugFromURL(rec.URL), "workerID", rec.WorkerID)
	return nil
}

// unrouteUserSessions unroutes the live sessions of the named users that are
// still routed to workerID, dropping their session-user join. Shared by the
// container-mode removal paths and DeprovisionRepo's bare-mode tail (which
// previously inlined this loop).
func (s *Server) unrouteUserSessions(workerID string, users map[string]struct{}) {
	for _, sid := range s.reg.SessionsForWorker(workerID) {
		user, ok := s.sessionUsers.Load(sid)
		if !ok {
			continue
		}
		name, _ := user.(string)
		if name == "" {
			continue
		}
		if _, hit := users[name]; hit {
			s.reg.UnrouteSession(sid)
			s.sessionUsers.Delete(sid)
		}
	}
}

// Placements is the panel-facing snapshot of container-mode placements.
func (s *Server) Placements() ([]Placement, error) {
	if s.placements == nil {
		return nil, nil
	}
	return s.placements.list()
}

// ReapProvisioning is the reaper tick: fail placements stuck provisioning
// past the register timeout (container created, worker never registered).
// The container itself is left running so the operator can inspect it —
// reconciliation (Phase C) reports orphans.
func (s *Server) ReapProvisioning(ctx context.Context, now time.Time) ([]Placement, error) {
	if s.incus == nil {
		return nil, nil
	}
	reaped, err := s.placements.reapProvisioning(s.incus.RegisterTimeout, now)
	for _, p := range reaped {
		s.logger.Warn("placement failed: worker never registered", "container", p.ContainerName,
			"user", p.User, "repo", p.RepoSlug, "age", now.Sub(p.CreatedAt).Round(time.Second))
	}
	return reaped, err
}

// placementSeed assembles the cloud-init seed for a container.
func (s *Server) placementSeed(name, repoURL, baseBranch string) incus.Seed {
	return incus.Seed{
		MasterURL:     s.incus.MasterURL,
		PairingSecret: s.incus.PairingSecret,
		RepoURL:       repoURL,
		RepoSlug:      repoSlugFromURL(repoURL),
		WorkerID:      name,
		BaseBranch:    baseBranch,
	}
}

// containerName derives the container name for a (repo, user) assignment —
// the byte-identical mirror of scripts/incus/assign.sh's name math
// (NAME_BUDGET=40). The repo segment is trimmed first (routeLabel's rule); the
// user segment survives whole. Only the NAME folds; the clone dir keeps the
// raw repo slug so placement keys stay byte-identical to safeRepoName.
func containerName(namePrefix, repoURL, userName string) string {
	const nameBudget = 40
	fold := func(s string) string {
		var b strings.Builder
		for _, ch := range s {
			switch {
			case ch >= 'A' && ch <= 'Z':
				b.WriteRune(ch - 'A' + 'a')
			case ch == '.' || ch == '_':
				b.WriteRune('-')
			default:
				b.WriteRune(ch)
			}
		}
		s = b.String()
		for strings.Contains(s, "--") {
			s = strings.ReplaceAll(s, "--", "-")
		}
		return strings.Trim(s, "-")
	}
	repo := fold(repoSlugFromURL(repoURL))
	if repo == "" {
		repo = "repo"
	}
	user := fold(userWorktreeSlug(userName))
	if user == "" {
		user = "user"
	}
	budget := nameBudget - len(namePrefix) - 1 - len(user)
	if budget < 1 {
		budget = nameBudget - len(namePrefix) - 1
	}
	if len(repo) > budget {
		repo = fold(repo[:budget])
		if repo == "" {
			repo = "repo"
		}
	}
	return namePrefix + repo + "-" + user
}
