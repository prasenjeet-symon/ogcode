package permission

import (
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/id"
)

type PermissionID = id.PermissionID

func NewPermissionID() PermissionID { return id.NewPermissionID() }

type Action string

const (
	Allow Action = "allow"
	Deny  Action = "deny"
	Ask   Action = "ask"
)

type Rule struct {
	Permission string `json:"permission"` // tool name or "edit", "*"
	Pattern    string `json:"pattern"`    // glob pattern
	Action     Action `json:"action"`
}

type Ruleset []Rule

type Request struct {
	ID        PermissionID
	SessionID string
	Tool      string
	Input     string
	Patterns  []string
}

// Evaluate checks the ruleset and returns the action for the given tool and path.
func (rs Ruleset) Evaluate(toolName, path string) Action {
	for _, rule := range rs {
		if rule.Permission == "*" || rule.Permission == toolName {
			if rule.Pattern == "*" || matchGlob(rule.Pattern, path) {
				return rule.Action
			}
		}
	}
	return Ask // default: ask the user
}

// DefaultRuleset returns the default permission rules for the build agent. The
// trailing catch-all Allow means only the explicitly-gated mutating tools
// (bash, write, edit) prompt; every other tool (read, search, memory, etc.)
// runs without interruption.
func DefaultRuleset() Ruleset {
	return Ruleset{
		{Permission: "read", Pattern: "*", Action: Allow},
		{Permission: "glob", Pattern: "*", Action: Allow},
		{Permission: "grep", Pattern: "*", Action: Allow},
		{Permission: "bash", Pattern: "*", Action: Ask},
		{Permission: "write", Pattern: "*", Action: Ask},
		{Permission: "edit", Pattern: "*", Action: Ask},
		{Permission: "*", Pattern: "*", Action: Allow},
	}
}

// PendingRequest holds a permission request awaiting user reply.
type PendingRequest struct {
	Request Request
	ReplyCh chan string // "once", "always", "reject"
}

// Manager manages pending permission requests and per-session rulesets. It is
// safe for concurrent use: the loop goroutine calls Create/Remove/AddRule while
// the HTTP handler goroutine calls Reply.
type Manager struct {
	mu       sync.Mutex
	pending  map[PermissionID]*PendingRequest
	rulesets map[string]Ruleset // sessionID -> ruleset ("always" grants append here)
}

func NewManager() *Manager {
	return &Manager{
		pending:  make(map[PermissionID]*PendingRequest),
		rulesets: make(map[string]Ruleset),
	}
}

func (m *Manager) Create(req Request) *PendingRequest {
	pr := &PendingRequest{
		Request: req,
		ReplyCh: make(chan string, 1),
	}
	m.mu.Lock()
	m.pending[req.ID] = pr
	m.mu.Unlock()
	return pr
}

func (m *Manager) Get(id PermissionID) *PendingRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[id]
}

// Remove discards a pending request without replying — used when the request is
// abandoned (e.g. the agent loop was cancelled while waiting for approval).
func (m *Manager) Remove(id PermissionID) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

func (m *Manager) Reply(id PermissionID, response string) bool {
	m.mu.Lock()
	pr := m.pending[id]
	if pr != nil {
		delete(m.pending, id)
	}
	m.mu.Unlock()
	if pr == nil {
		return false
	}
	// ReplyCh is buffered (cap 1), so this never blocks even if the waiter has
	// already given up (e.g. on ctx cancellation).
	pr.ReplyCh <- response
	return true
}

// Ruleset returns the effective ruleset for a session — the stored one if any
// "always" grants have been recorded, otherwise the default.
func (m *Manager) Ruleset(sessionID string) Ruleset {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rs, ok := m.rulesets[sessionID]; ok {
		return rs
	}
	return DefaultRuleset()
}

// AddRule prepends a grant to a session's ruleset so it takes precedence over
// the defaults. Used to honor an "always allow" reply for the rest of a session.
func (m *Manager) AddRule(sessionID string, rule Rule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	base, ok := m.rulesets[sessionID]
	if !ok {
		base = DefaultRuleset()
	}
	m.rulesets[sessionID] = append(Ruleset{rule}, base...)
}

// matchGlob does simple glob matching (* matches any sequence).
func matchGlob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	// Simple implementation: only handle exact match or * wildcard
	if pattern == s {
		return true
	}
	return false
}