package task

// Task statuses
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Effort levels
const (
	EffortS  = "S"
	EffortM  = "M"
	EffortL  = "L"
	EffortXL = "XL"
)

// Complexity levels
const (
	ComplexityLow    = "low"
	ComplexityMedium = "medium"
	ComplexityHigh   = "high"
)

// Task represents a unit of work derived from a locked Plan.
// Each task is executed in its own git branch with an isolated agent session.
type Task struct {
	ID           string   `json:"id"`
	PlanID       string   `json:"planId"`
	SessionID    *string  `json:"sessionId,omitempty"`
	ParentTaskID *string  `json:"parentTaskId,omitempty"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Effort       string   `json:"effort"`       // S, M, L, XL
	Complexity   string   `json:"complexity"`   // low, medium, high
	Status       string   `json:"status"`       // pending, in_progress, completed, failed
	Dependencies []string `json:"dependencies"` // task IDs this task depends on
	BranchName   string   `json:"branchName"`
	ChainBranch  string   `json:"chainBranch,omitempty"` // shared branch for a dependency chain; empty for standalone tasks
	WorktreePath string   `json:"worktreePath,omitempty"`
	PRURL        string   `json:"prUrl,omitempty"`
	PRNumber     *int     `json:"prNumber,omitempty"`
	PRError      string   `json:"prError,omitempty"` // non-empty when PR creation was skipped or failed
	Model        string   `json:"model,omitempty"`   // per-task model override; empty = inherit the plan's model
	// Provider is the provider that serves Model (or, when Model is empty, the
	// inherited one). Empty = resolve from the model id.
	Provider   string `json:"provider,omitempty"`
	OrderIndex int    `json:"orderIndex"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}
