package session

import (
	"github.com/prasenjeet-symon/ogcode/internal/id"
)

type SessionID = id.SessionID
type MessageID = id.MessageID
type PartID = id.PartID
type PermissionID = id.PermissionID

func NewSessionID() SessionID   { return id.NewSessionID() }
func NewMessageID() MessageID   { return id.NewMessageID() }
func NewPartID() PartID         { return id.NewPartID() }
func NewPermissionID() PermissionID { return id.NewPermissionID() }

type ModelPreference struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	ProviderID  string `json:"providerId"`
	DisplayName string `json:"displayName"`
	IsCustom    bool   `json:"isCustom"`
	// Collection is an optional group name for custom models so OpenAI-compatible
	// providers added through the OpenAI provider (Gemini, DeepSeek, Groq, …) can
	// be grouped together in the UI instead of all collapsing under "OpenAI".
	// Empty for built-in models and legacy custom models (falls back to providerId).
	Collection string `json:"collection"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// ModelCapability is a probed/known capability record for a model, persisted so
// the image-support probe runs at most once per model (until manually refreshed).
type ModelCapability struct {
	ModelID        string `json:"modelId"`
	SupportsImages bool   `json:"supportsImages"`
	ProbedAt       int64  `json:"probedAt"`
}