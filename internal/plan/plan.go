package plan

// Plan statuses
const (
	StatusOpen   = "open"
	StatusLocked = "locked"
)

// Breakdown statuses
const (
	BreakdownNone       = ""
	BreakdownInProgress = "in_progress"
	BreakdownCompleted  = "completed"
	BreakdownFailed     = "failed"
)

// Plan represents a planning session — a collaborative conversation
// between the user and the PlanAgent. Once locked, no further messages
// can be added. The plan serves as the reference point for all derived tasks.
type Plan struct {
	ID                string `json:"id"`
	SessionID         string `json:"sessionId"`
	ProjectID         string `json:"projectId"`
	Directory         string `json:"directory"`
	Title             string `json:"title"`
	Status            string `json:"status"`            // "open" | "locked"
	Model             string `json:"model,omitempty"`
	CompactionSummary string `json:"compactionSummary,omitempty"`
	BreakdownStatus    string `json:"breakdownStatus,omitempty"`    // "" | "in_progress" | "completed" | "failed"
	BreakdownWarnings  string `json:"breakdownWarnings,omitempty"`  // non-empty when some tasks failed to create
	AllTasksCompleted  bool   `json:"allTasksCompleted,omitempty"`  // true when locked and all tasks done
	Archived           bool   `json:"archived,omitempty"`           // true when ArchivedAt > 0
	CreatedAt          int64  `json:"createdAt"`
	UpdatedAt          int64  `json:"updatedAt"`
	ArchivedAt         int64  `json:"archivedAt,omitempty"`
}