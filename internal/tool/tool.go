package tool

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// ToolDef is the interface every tool must implement.
type ToolDef interface {
	ID() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error)
}

// Context is passed to every tool execution.
type Context struct {
	SessionID  session.SessionID
	MessageID  session.MessageID
	Agent      string
	CallID     string
	Ctx        context.Context
	SessionDir string
	Ask        func(req PermissionRequest) error
	Metadata   func(meta MetadataUpdate) error
	// ModelSupportsImages is true when the session's active model accepts image
	// input. Tools may use this to decide whether to return an image (e.g. a
	// rendered PDF page) instead of text.
	ModelSupportsImages bool
	// Model is the model ID the parent session is using. Tools that spawn child
	// sessions (e.g. deep_search) should inherit this so they run on the same model.
	Model string
}

// PermissionRequest is sent when a tool needs user approval.
type PermissionRequest struct {
	ID        session.PermissionID
	SessionID session.SessionID
	Tool      string
	Input     string
}

// MetadataUpdate updates the running tool call's display metadata.
type MetadataUpdate struct {
	Title    string         `json:"title,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Result is returned from tool execution.
type Result struct {
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Output   string         `json:"output"`
	// Image, when non-nil, is an image the tool wants the model to see (e.g. a
	// rendered PDF page). It is delivered to the model alongside Output, in a
	// provider-appropriate way. Only honored for vision-capable models.
	Image *ResultImage `json:"image,omitempty"`
	// Truncated marks that the tool already capped its own Output to a safe size.
	// The agent loop's global truncation backstop leaves such results untouched;
	// results without this flag are capped to MaxToolOutputBytes/MaxToolOutputLines
	// before they enter the model context.
	Truncated bool `json:"truncated,omitempty"`
	// Denied marks that the tool call was rejected by the permission gate and was
	// never executed. The loop uses it to record a ToolDenied status (distinct
	// from ToolCompleted/ToolError) so the UI and DB reflect that the call was
	// blocked rather than run.
	Denied bool `json:"-"`
}

// ResultImage is an image attachment on a tool Result.
// Data is base64-encoded image bytes; MediaType is e.g. "image/jpeg".
type ResultImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

// Registry holds all registered tools.
//
// A mutex guards the map because MCP server connections are established lazily
// in a background goroutine (after the HTTP server starts) and register their
// tools into the same registry the agent loop reads each step via ForAgent/Get.
// Without the lock that concurrent Register+read is a data race.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolDef
	// gen counts membership changes. A turn resolves its toolset once and then
	// re-resolves only when this moves, so the tool array it sends is
	// byte-identical on every step that did not actually see a change — which is
	// what the providers' prompt caches require (on Anthropic tool definitions
	// lead the cache prefix, so any churn there invalidates the system block and
	// the message history too).
	gen uint64
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolDef)}
}

func (r *Registry) Register(t ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.ID()] = t
	// Bumped unconditionally: re-registering an id can replace its definition,
	// and ToolDef is an interface so the old and new are not comparable. A
	// redundant bump costs one re-resolve, which is the cheap direction to err in.
	r.gen++
}

// Remove deletes tools by id. It is how a disabled MCP server's tools stop
// reaching the agent: the next ForAgent no longer expands "mcp_*" onto them, so
// their schemas leave the prompt. Unknown ids are ignored.
func (r *Registry) Remove(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if _, ok := r.tools[id]; ok {
			delete(r.tools, id)
			// Only a real deletion counts. Remove is called speculatively with
			// ids that may not be registered, and bumping for those would make a
			// turn re-resolve — and take a cache miss — for nothing.
			r.gen++
		}
	}
}

// Generation reports a counter bumped on every membership change. It exists so a
// caller can hold a resolved toolset and cheaply detect that it went stale,
// instead of choosing between re-resolving every time (which churns the cached
// prompt prefix) and never re-resolving (which leaves a long turn permanently
// blind to a server that connected after it started).
func (r *Registry) Generation() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gen
}

func (r *Registry) Get(id string) ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[id]
}

func (r *Registry) List() []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ForAgent resolves the requested tool ids against the registry. An id is
// matched exactly first; if that fails it is treated as a glob ("*" wildcard)
// and expanded against every registered id, so "mcp_*" selects all MCP tools
// whatever servers expose them. Each matched tool appears once even if two
// patterns overlap.
func (r *Registry) ForAgent(toolIDs []string) []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	var result []ToolDef
	add := func(t ToolDef) {
		if seen[t.ID()] {
			return
		}
		seen[t.ID()] = true
		result = append(result, t)
	}
	for _, id := range toolIDs {
		if t, ok := r.tools[id]; ok {
			add(t)
			continue
		}
		// Glob expansion: "mcp_*" → every id matching the pattern. Only patterns
		// containing "*" are treated as globs; a plain id with no match (and no
		// "*") is silently dropped to preserve the existing exact-match behavior.
		if !strings.Contains(id, "*") {
			continue
		}
		pattern := strings.ReplaceAll(id, "*", ".*")
		re, err := regexp.Compile("^" + pattern + "$")
		if err != nil {
			continue
		}
		// Sorted, never map order. Ranging a Go map is randomized on every
		// call, so expanding a glob straight into the result hands the provider
		// a differently-ordered tool array on every step of a turn — and tool
		// definitions are part of the cached prompt prefix on every provider we
		// support. On Anthropic the prefix is ordered tools → system → messages,
		// so a reshuffle invalidates the tool block, the cache_control'd system
		// block, AND the whole message history on every step; it also moves the
		// breakpoint anthropic.go pins to the LAST tool onto a different tool
		// each time. OpenAI documents reordering as breaking the prefix too.
		//
		// The id is the only stable key here (Description/Parameters are derived
		// from it), and skill.Registry.Visible() already sorts for the same
		// reason — this is the tool registry catching up with it.
		matched := make([]string, 0, len(r.tools))
		for tid := range r.tools {
			if re.MatchString(tid) {
				matched = append(matched, tid)
			}
		}
		sort.Strings(matched)
		for _, tid := range matched {
			add(r.tools[tid])
		}
	}
	return result
}

// ToProviderTools converts tool definitions to provider format.
func ToProviderTools(tools []ToolDef) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		result = append(result, map[string]any{
			"name":        t.ID(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
		})
	}
	return result
}
