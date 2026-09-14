package worker

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/tunnel"
)

// openTunnel opens the byte-pipe stream to the master for ONE worktree server
// and serves the yamux end of it, splicing every stream the master opens to
// that worktree's local ogcode web server on 127.0.0.1:<port>. The master keys
// the tunnel under "<workerID>-<route>" (route = routeLabel(dir)), so one
// worker can serve several worktree UIs at distinct panel subdomains.
//
// The first frame carries the worker token in data (it authenticates the tunnel
// AND forces Connect to dispatch the request — a bidi request is not delivered
// until the client sends) and the route label in route. The master consumes it
// before yamux starts, so it never enters the multiplexed byte stream.
//
// The tunnel is retried while the server lives: a dropped master connection
// re-handshakes (the master reads the token fresh from the first frame), with a
// pause between attempts so a downed master doesn't spin the CPU.
func (w *Worker) openTunnel(ctx context.Context, dir string, h *serverHost) {
	if w.client == nil {
		// No connection to the master (tests, or startAgent run outside a
		// session) — nothing to tunnel through.
		return
	}
	route := routeLabel(dir)
	first := true
	for {
		if ctx.Err() != nil {
			return
		}
		if !first {
			if !sleepBackoff(ctx, 2*time.Second) {
				return
			}
		}
		first = false

		stream := w.client.Tunnel(ctx)
		err := stream.Send(&cpv1.TunnelChunk{
			Data:  []byte(w.getToken()),
			Route: route,
		})
		if err != nil {
			w.logger.Warn("tunnel: handshake send failed", "dir", dir, "err", err)
			_ = stream.CloseRequest()
			continue
		}

		sess, err := tunnel.ServerSession(stream, func() {
			_ = stream.CloseRequest()
			_ = stream.CloseResponse()
		})
		if err != nil {
			w.logger.Warn("tunnel: yamux server failed", "dir", dir, "err", err)
			continue
		}
		w.logger.Info("tunnel open to master", "dir", dir, "route", route, "port", h.port)

		addr := fmt.Sprintf("127.0.0.1:%d", h.port)
		for {
			conn, err := sess.Accept()
			if err != nil {
				if ctx.Err() == nil {
					w.logger.Warn("tunnel: accept ended; reconnecting", "dir", dir, "err", err)
				}
				sess.Close()
				break
			}
			go w.spliceToLocal(conn, addr)
		}
	}
}

// spliceToLocal pipes one tunneled connection to the worktree's local ogcode.
func (w *Worker) spliceToLocal(remote net.Conn, localAddr string) {
	defer remote.Close()
	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		w.logger.Warn("tunnel: dial local ogcode failed", "addr", localAddr, "err", err)
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	<-done // first side to finish; the defers close both, unblocking the other
}

// worktreeLabel turns a worktree directory into a DNS-safe route label for the
// master's composite tunnel key "<workerID>-<label>" — the master splits the
// key on the FIRST hyphen, so the label itself may contain hyphens but the
// worker id (base32, no hyphens) must stay unambiguous, which loadOrCreateWorkerID
// already guarantees.
func worktreeLabel(dir string) string {
	base := filepath.Base(strings.TrimRight(filepath.Clean(dir), string(filepath.Separator)))
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	label := strings.Trim(b.String(), "-")
	if label == "" {
		label = "w"
	}
	if len(label) > 63 {
		label = label[:63]
	}
	return label
}

// routeLabel derives the DNS-safe tunnel route for a worktree directory. For a
// USER worktree — the shape EnsureUserWorktree creates,
// "<repoDir>/.ogcode/worktrees/user/<name>" — it qualifies the route with the
// repository slug, "<reposlug>-<name>", so one user's worktrees in different
// repositories never collide on the same worker (two bare "<name>" routes would
// register the same "<workerID>-<name>" key on the master and flap, evicting
// each other). Every other directory — a --workspace root, a bare clone, or any
// non-user path — keeps the plain base-name label worktreeLabel produces, so
// existing single-workspace routes are unchanged.
//
// The composite is capped at the RFC 1123 label limit (63 bytes); when it would
// overflow, the repo segment is trimmed first so the user segment (the part an
// operator recognizes) always survives intact.
func routeLabel(dir string) string {
	clean := filepath.Clean(dir)
	repoSlug, name, ok := userWorktreeParts(clean)
	if !ok {
		return worktreeLabel(clean)
	}
	userPart := worktreeLabel(name)
	repoPart := worktreeLabel(repoSlug)
	if repoPart == "" {
		return userPart
	}
	const maxLabel = 63
	if len(repoPart)+1+len(userPart) > maxLabel {
		budget := maxLabel - 1 - len(userPart)
		if budget < 0 {
			budget = 0
		}
		// In this branch len(repoPart) > budget, so the slice is always in range.
		repoPart = strings.Trim(repoPart[:budget], "-")
		if repoPart == "" {
			return worktreeLabel(name) // repo budget exhausted; fall back to the user route
		}
	}
	return repoPart + "-" + userPart
}

// userWorktreeParts recognizes the user-worktree directory shape
// EnsureUserWorktree creates — "<repoDir>/.ogcode/worktrees/user/<name>" — and
// returns the repo directory's base name (the repo slug) and the user segment.
// ok is false for any directory that is not this exact shape. clean must already
// be filepath.Clean'd.
func userWorktreeParts(clean string) (repoSlug, name string, ok bool) {
	name = filepath.Base(clean)
	p := filepath.Dir(clean) // .../worktrees/user
	if filepath.Base(p) != "user" {
		return "", "", false
	}
	p = filepath.Dir(p) // .../worktrees
	if filepath.Base(p) != "worktrees" {
		return "", "", false
	}
	p = filepath.Dir(p) // .../.ogcode
	if filepath.Base(p) != ".ogcode" {
		return "", "", false
	}
	repoSlug = filepath.Base(filepath.Dir(p)) // <repoDir>'s base name
	if !meaningfulSegment(name) || !meaningfulSegment(repoSlug) {
		return "", "", false
	}
	return repoSlug, name, true
}

// meaningfulSegment reports whether a path segment names a real directory
// component (not empty, ".", "..", or a filesystem root separator).
func meaningfulSegment(seg string) bool {
	switch seg {
	case "", ".", "..", string(filepath.Separator):
		return false
	}
	return true
}
