package tool

import (
	"context"
	"encoding/json"

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
}

// ResultImage is an image attachment on a tool Result.
// Data is base64-encoded image bytes; MediaType is e.g. "image/jpeg".
type ResultImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

// Registry holds all registered tools.
type Registry struct {
	tools map[string]ToolDef
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolDef)}
}

func (r *Registry) Register(t ToolDef) {
	r.tools[t.ID()] = t
}

func (r *Registry) Get(id string) ToolDef {
	return r.tools[id]
}

func (r *Registry) List() []ToolDef {
	var result []ToolDef
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

func (r *Registry) ForAgent(toolIDs []string) []ToolDef {
	var result []ToolDef
	for _, id := range toolIDs {
		if t, ok := r.tools[id]; ok {
			result = append(result, t)
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