package master

import (
	"net/http"
	"strings"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Operator scoping: what the account behind a request may see on the console.
// Three tiers — administrators (every worker and session), repo-scoped users
// (their own worktree's worker and only sessions started under their own
// account), and the pre-accounts modes (legacy password / disabled gate), which
// stay unrestricted exactly as before per-user accounts shipped.
//
// The store is always the source of truth: allowlists are read live at request
// time (WorkspacesForUser), and assignment persists its scoping pin at
// AssignUser time — so changes take effect without a re-login beyond the
// cookie-fingerprint invalidation every allowlist write triggers.

// currentUser resolves the operator account behind the request: its id and
// whether it is an administrator. ok=false means the request carries no usable
// account session (callers fail closed). A disabled gate or legacy mode yields
// ("", true, true): no user ids exist there and nothing is scoped.
func (s *Server) currentUser(r *http.Request) (userID string, admin bool, ok bool) {
	if s.gate == nil || !s.gate.Enabled() || s.gate.Mode() != auth.ModeAccounts {
		return "", true, true
	}
	id, valid := s.gate.UserForSession(r)
	if !valid || id == "" {
		return "", false, false
	}
	admin = false
	if store := s.reg.Store(); store != nil {
		if rec, found, err := store.GetUser(id); err == nil && found {
			admin = rec.IsAdmin()
		}
		// decodeSession already verified the account still exists (A1); a
		// miss here is a vanishing race — resolve as non-admin, fail closed.
	}
	return id, admin, true
}

// sessionVisible reports whether the current operator may open the monitor for
// sessionID. Administrators see everything; a scoped user only the sessions
// started under their own account. Unknown sessions fall through to the
// registry check so the caller renders its normal not-found page.
func (s *Server) sessionVisible(r *http.Request, sessionID string) bool {
	userID, admin, ok := s.currentUser(r)
	if !ok {
		return false
	}
	if admin {
		return true
	}
	startedBy, scoped := s.SessionUser(sessionID)
	return scoped && startedBy == userID
}

// visibleWorkers filters the worker list for the console's front page.
// Administrators see every worker; a scoped account sees only workers
// advertising a workspace its allowlist names — the same match the UI proxy
// enforces on the tunnel itself, so the console never links a worker the
// account could not actually open.
func (s *Server) visibleWorkers(r *http.Request, infos []registry.WorkerInfo) []registry.WorkerInfo {
	userID, admin, ok := s.currentUser(r)
	if !ok {
		return nil // fail closed
	}
	if admin {
		return infos
	}
	store := s.reg.Store()
	if store == nil {
		return nil
	}
	rec, found, err := store.GetUser(userID)
	if err != nil || !found || len(rec.Workspaces) == 0 {
		return nil // a scoped account with no allowlist sees nothing
	}
	out := make([]registry.WorkerInfo, 0, len(infos))
	for _, info := range infos {
		if workerOffers(info, rec.Workspaces) {
			out = append(out, info)
		}
	}
	return out
}

// workerOffers reports whether a worker advertises a workspace matching any
// allowlist identifier: a workspace NAME equal to it, or a PATH equal to it or
// ending with it as a segment (mirrors tunnelMatchesAllowlist's workspace
// rules; the route-label rule does not apply here because the front page lists
// workers, not tunnels).
func workerOffers(info registry.WorkerInfo, allowed []string) bool {
	for _, want := range allowed {
		for _, ws := range info.Workspaces {
			if ws.Name == want || ws.Path == want || strings.HasSuffix(ws.Path, "/"+want) {
				return true
			}
		}
	}
	return false
}
