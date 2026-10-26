package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MaxDelaySeconds caps a create's delaySeconds at 366 days. The bound is a
// correctness guard as much as a policy: seconds×1e9 is the nanosecond Duration
// the handler builds the fire time from, and past this the product wraps
// negative — a reminder that fires instantly instead of never. A further
// deadline belongs in fireAt, which carries an absolute instant and has no
// such arithmetic.
const MaxDelaySeconds = 31_622_400

// ScheduleFunc carries out one schedule operation for the calling session —
// creating, listing or cancelling a one-shot reminder. Implemented by the
// server and wired in from server.go to avoid the tool→server import cycle.
type ScheduleFunc func(ctx context.Context, sessionID string, req ScheduleRequest) (ScheduleResult, error)

// ScheduleRequest is one operation on this session's reminders.
type ScheduleRequest struct {
	// Op is create, list or cancel.
	Op string `json:"op"`
	// DelaySeconds is how long from now a create fires. Exactly one of
	// DelaySeconds and FireAt is required on create.
	DelaySeconds int `json:"delaySeconds,omitempty"`
	// FireAt is an absolute RFC3339 time a create fires. Exactly one of
	// DelaySeconds and FireAt is required on create.
	FireAt string `json:"fireAt,omitempty"`
	// Note is what the reminder says when it fires; create requires it.
	Note string `json:"note,omitempty"`
	// ID is the reminder a cancel removes.
	ID string `json:"id,omitempty"`
}

// ScheduledTimer is one pending reminder as a list reports it.
type ScheduledTimer struct {
	ID     string `json:"id,omitempty"`
	Note   string `json:"note,omitempty"`
	FireAt string `json:"fireAt,omitempty"`
	Status string `json:"status,omitempty"`
}

// ScheduleResult is what one schedule operation produced.
type ScheduleResult struct {
	// ID and FireAt echo the reminder a create or cancel acted on. For a
	// delay-form create the handler fills FireAt as now+DelaySeconds in
	// RFC3339.
	ID     string `json:"id,omitempty"`
	FireAt string `json:"fireAt,omitempty"`
	// Timers are the reminders a list reports.
	Timers []ScheduledTimer `json:"timers,omitempty"`
}

// ScheduleTool sets a one-shot reminder that is delivered back into the
// session's loop as a system notice when its deadline passes.
type ScheduleTool struct{ Run ScheduleFunc }

func (ScheduleTool) ID() string { return "schedule" }

func (ScheduleTool) Description() string {
	return "Set a one-shot reminder for this session: a scheduled note that comes back when its deadline passes and is delivered into the session as a system notice you can act on or relay. " +
		"It never starts a turn — if the session is idle when the reminder fires, the note shows up at the start of the next turn. " +
		"`create` schedules one from a self-contained `note` plus exactly one of `delaySeconds` (seconds from now) or `fireAt` (an RFC3339 timestamp), and returns the reminder's id. " +
		"`cancel` removes a pending reminder by the id create returned; `list` shows the reminders still pending. " +
		"The note must carry enough context to stand on its own — by the time it fires you may be in a different turn."
}

func (ScheduleTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"op": {
				"type": "string",
				"enum": ["create", "list", "cancel"],
				"description": "The operation — create schedules a one-shot reminder from note + exactly one of delaySeconds/fireAt, list shows the reminders still pending, cancel removes one by id. Required."
			},
			"delaySeconds": {
				"type": "integer",
				"description": "create: how many seconds from now the reminder fires, capped at 366 days. Exactly one of delaySeconds or fireAt is required; for a later deadline use fireAt."
			},
			"fireAt": {
				"type": "string",
				"description": "create: the absolute RFC3339 time the reminder fires, e.g. 2026-10-24T15:04:05Z. Exactly one of delaySeconds or fireAt is required."
			},
			"note": {
				"type": "string",
				"description": "create: what the reminder says — delivered back into the session verbatim when it fires. Required, and self-contained."
			},
			"id": {
				"type": "string",
				"description": "cancel: the reminder id create returned."
			}
		},
		"required": ["op"]
	}`)
}

func (t ScheduleTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var req ScheduleRequest
	if err := DecodeArgs(args, &req); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	req.Op = strings.TrimSpace(req.Op)

	switch req.Op {
	case "create":
		if out, ok := validateScheduleCreate(req); !ok {
			return Result{Output: out}, nil
		}
	case "list":
	case "cancel":
		if strings.TrimSpace(req.ID) == "" {
			return Result{Output: "id is required to cancel a reminder — use the id create returned"}, nil
		}
	case "":
		return Result{Output: missingScheduleOpHint(req)}, nil
	default:
		return Result{Output: fmt.Sprintf("unknown op %q — use create, list or cancel", req.Op)}, nil
	}

	if t.Run == nil {
		return Result{Output: "reminders are not available in this environment"}, nil
	}

	res, err := t.Run(ctx, string(tctx.SessionID), req)
	if err != nil {
		return Result{Title: "schedule", Output: fmt.Sprintf("Schedule error: %s", err)}, nil
	}
	return Result{Title: "schedule: " + req.Op, Output: formatSchedule(req.Op, res)}, nil
}

// validateScheduleCreate enforces create's requirements: a non-empty note,
// and exactly one fire time between delaySeconds (positive, at most
// MaxDelaySeconds) and a parseable, strictly future RFC3339 fireAt. It returns
// (refusal, false) with the text the tool should print, or ("", true) when the
// request may pass to the handler — which may still clamp the fire time as it
// likes.
func validateScheduleCreate(req ScheduleRequest) (string, bool) {
	if strings.TrimSpace(req.Note) == "" {
		return "note is required — say what the reminder is for", false
	}
	delay := req.DelaySeconds > 0
	fire := req.FireAt != ""
	switch {
	case delay && fire:
		return "create takes exactly one of delaySeconds or fireAt — both were given", false
	// A negative delaySeconds is its own caller slip — "neither was given"
	// would be the wrong diagnosis, and the wording must match the server's
	// reminder handling byte for byte.
	case req.DelaySeconds < 0:
		return "delaySeconds must be a positive number of seconds", false
	case !delay && !fire:
		return "create takes exactly one of delaySeconds or fireAt — neither was given", false
	case delay && req.DelaySeconds > MaxDelaySeconds:
		return fmt.Sprintf("delaySeconds %d is too long — the cap is %d seconds (366 days); use fireAt for a further deadline", req.DelaySeconds, MaxDelaySeconds), false
	case fire:
		when, err := time.Parse(time.RFC3339, req.FireAt)
		if err != nil {
			return fmt.Sprintf("fireAt must be RFC3339 (e.g. 2026-10-24T15:04:05Z): %s", req.FireAt), false
		}
		if !when.After(time.Now()) {
			return fmt.Sprintf("fireAt must be in the future: %s", req.FireAt), false
		}
	}
	return "", true
}

// missingScheduleOpHint turns an op-less call into a hint the model can act
// on: every schedule argument belongs to create, so the field it did send
// names the operation it probably meant.
func missingScheduleOpHint(req ScheduleRequest) string {
	if req.Note != "" || req.DelaySeconds > 0 || req.FireAt != "" {
		return `"op" is required — note, delaySeconds and fireAt are create arguments: resend the call with "op": "create", a note, and exactly one of "delaySeconds" or "fireAt"`
	}
	return `op is required — use create, list or cancel (create takes a note plus exactly one of delaySeconds or fireAt)`
}

// formatSchedule renders one schedule result: the reminder it echoed on a
// create or cancel, or the pending list a list returned.
func formatSchedule(op string, res ScheduleResult) string {
	var b strings.Builder
	if res.ID != "" {
		switch op {
		case "cancel":
			fmt.Fprintf(&b, "Reminder %s cancelled.", res.ID)
		default:
			fmt.Fprintf(&b, "Reminder %s set.", res.ID)
			if res.FireAt != "" {
				fmt.Fprintf(&b, " Fires %s.", res.FireAt)
			}
		}
		b.WriteString("\n")
	}
	if len(res.Timers) > 0 {
		fmt.Fprintf(&b, "\nPending reminders (%d):\n", len(res.Timers))
		for _, timer := range res.Timers {
			fmt.Fprintf(&b, "- %s", timer.ID)
			if timer.FireAt != "" {
				fmt.Fprintf(&b, " — fires %s", timer.FireAt)
			}
			if timer.Status != "" {
				fmt.Fprintf(&b, " (%s)", timer.Status)
			}
			if timer.Note != "" {
				fmt.Fprintf(&b, " — %s", timer.Note)
			}
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return "No reminders."
	}
	return strings.TrimRight(b.String(), "\n")
}
