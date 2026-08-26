package session

import (
	"encoding/json"
	"time"
)

type Session struct {
	ID                SessionID `json:"id"`
	ProjectID         string    `json:"projectId"`
	Directory         string    `json:"directory"`
	Title             string    `json:"title"`
	Model             string    `json:"model,omitempty"`
	SessionType       string    `json:"sessionType,omitempty"`
	Permission        string    `json:"permission,omitempty"`
	CompactionSummary string    `json:"compactionSummary,omitempty"`
	MemoryTokensSaved int       `json:"memoryTokensSaved,omitempty"`
	CreatedAt         int64     `json:"createdAt"`
	UpdatedAt         int64     `json:"updatedAt"`
}

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
)

type MessageInfo struct {
	ID        MessageID    `json:"id"`
	SessionID SessionID    `json:"sessionId"`
	Role      MessageRole  `json:"role"`
	Agent     string       `json:"agent,omitempty"`
	ParentID  *MessageID   `json:"parentId,omitempty"`
	Finish    *string      `json:"finish,omitempty"`
	Cost      float64      `json:"cost,omitempty"`
	Tokens    *TokenCounts `json:"tokens,omitempty"`
	Error     *string      `json:"error,omitempty"`
	// Interrupted is set when a loop stopped part-way through this turn rather
	// than because the model finished. It is what a resume decides from.
	Interrupted *Interruption `json:"interrupted,omitempty"`
	CreatedAt   int64         `json:"createdAt"`
}

// InterruptReason classifies why a turn stopped short.
type InterruptReason string

const (
	// InterruptRateLimit is a 429 or a provider quota, the case where waiting
	// is the whole fix. RetryAfter carries when waiting is over, where the
	// provider said.
	InterruptRateLimit InterruptReason = "rate_limit"
	// InterruptServerError is a 5xx or an overloaded provider.
	InterruptServerError InterruptReason = "server_error"
	// InterruptNetwork is a connection that dropped, timed out or was refused.
	InterruptNetwork InterruptReason = "network"
	// InterruptAuth is a rejected key, an expired token, an exhausted balance —
	// resumable, but only once a human has fixed the account behind it.
	InterruptAuth InterruptReason = "auth"
	// InterruptContext is a request too large for the model's window that
	// compaction could not bring back under it.
	InterruptContext InterruptReason = "context"
	// InterruptCrashed marks a turn found unfinished at startup: the process
	// died mid-stream and never got to record anything about why.
	InterruptCrashed InterruptReason = "crashed"
	// InterruptStalled marks a turn that recorded a finish reason but not one
	// the model chose — it asked for a tool and nothing ran it, or it hit the
	// output cap mid-answer. The loop that would have carried on is gone.
	InterruptStalled InterruptReason = "stalled"
	// InterruptFatal is everything a retry cannot help — a malformed request, a
	// model that does not exist, a provider rejecting the tool schema.
	InterruptFatal InterruptReason = "fatal"
)

// Interruption records why a turn stopped short of finishing and whether
// picking it up again is worth trying.
//
// It sits beside Error rather than replacing it. Error is the provider's own
// words, which a user needs to read; this is the classification the resume path
// acts on, and the two answer different questions.
type Interruption struct {
	Reason    InterruptReason `json:"reason"`
	Resumable bool            `json:"resumable"`
	// Detail is a short human-facing sentence naming what to do about it. The
	// raw provider error stays in Error.
	Detail string `json:"detail,omitempty"`
	// RetryAfter is the unix second the provider said to come back at, or 0
	// where it said nothing. Only a rate limit tends to carry one.
	RetryAfter int64 `json:"retryAfter,omitempty"`
	// Step is the loop step the turn died on, for the UI to say how far it got.
	Step int `json:"step,omitempty"`
}

// FinishedNaturally reports whether a finish reason means the model was done.
//
// Only three are: the model saying it finished, and the user saying stop. Every
// other reason — none recorded at all, a request for tools that nothing ran, an
// error, the output cap — describes a turn that stopped without reaching an
// end, which is what makes it something to pick back up.
//
// The default is deliberately "not finished". A finish reason this code has
// never seen is far more likely to be a provider spelling one of the failures
// its own way than a fourth kind of success, and the cost of the two mistakes
// is not symmetric: offering a resume that turns out to be unnecessary wastes a
// click, while withholding one strands the conversation.
func FinishedNaturally(finish *string) bool {
	if finish == nil {
		return false
	}
	switch *finish {
	case "stop", "end_turn", "aborted":
		return true
	}
	return false
}

// CanResume reports whether a message is one a resume should act on.
func (m *MessageInfo) CanResume() bool {
	return m != nil && m.Role == RoleAssistant && m.Interrupted != nil && m.Interrupted.Resumable
}

type TokenCounts struct {
	Total      int `json:"total,omitempty"`
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}

type MessageWithParts struct {
	Info  MessageInfo `json:"info"`
	Parts []Part      `json:"parts"`
}

type PartType string

const (
	PartText      PartType = "text"
	PartTool      PartType = "tool"
	PartReasoning PartType = "reasoning"
	PartFile      PartType = "file"
	PartImage     PartType = "image"
)

type Part struct {
	ID        PartID          `json:"id"`
	MessageID MessageID       `json:"messageId"`
	SessionID SessionID       `json:"sessionId"`
	Type      PartType        `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

type TextPartData struct {
	Text string `json:"text"`
}

// ImagePartData stores a user-uploaded image attachment. Data is base64-encoded
// image bytes; MediaType is e.g. "image/jpeg" or "image/png". The Name field
// carries the original filename (optional, for display only).
type ImagePartData struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
	Name      string `json:"name,omitempty"`
}

type ToolStatus string

const (
	ToolPending   ToolStatus = "pending"
	ToolRunning   ToolStatus = "running"
	ToolCompleted ToolStatus = "completed"
	ToolError     ToolStatus = "error"
	ToolDenied    ToolStatus = "denied"
)

type ToolPartData struct {
	Tool   string    `json:"tool"`
	CallID string    `json:"callId"`
	State  ToolState `json:"state"`
}

type ToolState struct {
	Status   ToolStatus      `json:"status"`
	Input    json.RawMessage `json:"input"`
	Output   *string         `json:"output,omitempty"`
	Error    *string         `json:"error,omitempty"`
	Title    *string         `json:"title,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Image    *ToolImage      `json:"image,omitempty"`
	Time     ToolTime        `json:"time"`
}

// ToolImage is an image produced by a tool, persisted so the model can be
// re-sent the image on history replay. Data is base64-encoded image bytes.
type ToolImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type ToolTime struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type ReasoningPartData struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

func Now() int64 {
	return time.Now().UnixMilli()
}
