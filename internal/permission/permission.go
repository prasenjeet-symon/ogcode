package permission

import (
	"log/slog"
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
	ID        PermissionID `json:"permissionId"`
	SessionID string       `json:"sessionId"`
	Tool      string       `json:"tool"`
	Input     string       `json:"input"`
	Patterns  []string     `json:"patterns"`
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

// EnsureRules seeds a session's ruleset with rules that come from
// configuration rather than from a user's reply — today, the per-skill
// allow/deny/ask rules in ogcode.json.
//
// It applies only to a session that has no ruleset yet. That makes it
// idempotent, so the agent loop can call it at the start of every turn, and it
// means an "always allow" grant the user gave earlier in the session is never
// overwritten by a config rule on the next turn.
//
// Rules are placed ahead of DefaultRuleset so they are reached before its
// trailing catch-all Allow, which would otherwise make every configured "ask"
// rule unreachable.
func (m *Manager) EnsureRules(sessionID string, rules Ruleset) {
	if len(rules) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rulesets[sessionID]; ok {
		return
	}
	m.rulesets[sessionID] = append(append(Ruleset{}, rules...), DefaultRuleset()...)
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
	mu        sync.Mutex
	pending   map[PermissionID]*PendingRequest
	rulesets  map[string]Ruleset // sessionID -> config-seeded rules (see EnsureRules)
	riskCache map[string]Risk    // Auto-mode LLM risk verdicts, keyed by command
	store     *Store             // global config DB; nil persists nothing
	global    Ruleset            // stored "always allow" grants, applied to every session
}

// NewManager returns a manager backed by store. A nil store (tests, headless
// runs) persists nothing: grants stay in-memory and session-scoped, exactly as
// they did before the store existed.
func NewManager(store *Store) *Manager {
	m := &Manager{
		pending:   make(map[PermissionID]*PendingRequest),
		rulesets:  make(map[string]Ruleset),
		riskCache: make(map[string]Risk),
		store:     store,
	}
	m.loadGrants()
	return m
}

// loadGrants reads the persisted "always allow" grants into memory so every
// session sees them. A read failure leaves the manager with none rather than
// failing construction: an unreadable database should degrade to asking, not to
// a server that will not start.
func (m *Manager) loadGrants() {
	if m.store == nil {
		return
	}
	grants, err := m.store.Grants()
	if err != nil {
		slog.Warn("failed to load stored permission grants", "err", err)
		return
	}
	m.global = grants
}

// CachedRisk returns a previously-computed Auto-mode risk verdict for a command.
func (m *Manager) CachedRisk(command string) (Risk, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.riskCache[command]
	return r, ok
}

// CacheRisk records an Auto-mode risk verdict for a command. Command risk is
// context-independent, so the cache is process-global. It is capped so a long
// session with many distinct commands can't grow it without bound.
func (m *Manager) CacheRisk(command string, r Risk) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.riskCache) >= 1000 {
		m.riskCache = make(map[string]Risk)
	}
	m.riskCache[command] = r
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

// PendingForSession returns all pending (unanswered) permission requests for a
// session, in creation order. The UI uses this to restore the permission queue
// when the user switches back to a session whose prompt was dismissed by the
// view change — the agent loop is still blocked on the reply.
func (m *Manager) PendingForSession(sessionID string) []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Request
	for _, pr := range m.pending {
		if pr.Request.SessionID == sessionID {
			out = append(out, pr.Request)
		}
	}
	return out
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

// Ruleset returns the effective ruleset for a session: the persisted "always
// allow" grants first, then the session's config-seeded rules, then the
// defaults. The stored grants come first because the user gave them explicitly,
// and a later configured ask would otherwise be shadowed by the catch-all Allow
// in the defaults.
func (m *Manager) Ruleset(sessionID string) Ruleset {
	m.mu.Lock()
	defer m.mu.Unlock()
	base, ok := m.rulesets[sessionID]
	if !ok {
		base = DefaultRuleset()
	}
	if len(m.global) == 0 {
		return base
	}
	return append(append(Ruleset{}, m.global...), base...)
}

// AddRule records an "always allow" grant. With a store attached the grant is
// durable and machine-wide: it is written once and reaches every session (this
// one included) through the merge in Ruleset, so there is a single source of
// truth for it. Without a store — tests, headless runs — it stays in-memory and
// session-scoped, as before. A failed write still grants it for this session, so
// an unwritable database costs the user a repeated prompt, not a lost approval.
func (m *Manager) AddRule(sessionID string, rule Rule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		if err := m.store.AddGrant(rule); err == nil {
			m.global = append(Ruleset{rule}, m.global...)
			return
		} else {
			slog.Warn("failed to persist permission grant; applying for this session only", "err", err)
		}
	}
	base, ok := m.rulesets[sessionID]
	if !ok {
		base = DefaultRuleset()
	}
	m.rulesets[sessionID] = append(Ruleset{rule}, base...)
}

// DefaultMode is the Ask/Auto/Yolo mode a newly created session starts in. It
// is a starting point, not a live setting: an existing session keeps the mode
// on its own row, so changing this does not move sessions already in progress.
// A nil manager (a server built without one, as some tests do) reads as Ask.
func (m *Manager) DefaultMode() string {
	if m == nil || m.store == nil {
		return ModeAsk
	}
	return m.store.DefaultMode()
}

// SetDefaultMode records the Ask/Auto/Yolo mode new sessions start in. Called
// when a user flips the mode toggle in any session — last choice wins.
func (m *Manager) SetDefaultMode(mode string) error {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.SetDefaultMode(mode)
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
