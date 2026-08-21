package agent

import (
	"encoding/json"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// resumeScanLimit bounds how far back a reconciliation looks. Only the tail of
// a session can be inconsistent — everything before the turn that died was
// completed and paired — so a few messages is enough, and reading the whole
// history to fix the last two of it would cost more the longer a session ran.
const resumeScanLimit = 20

// ReconcileSession repairs the tail of a session so the next request to the
// provider is valid, and returns the assistant message a resume would restart
// from, or nil when the session needs no resuming.
//
// The repair that matters is pairing tool calls. A turn that died between the
// model emitting tool_use blocks and the loop writing their results leaves the
// history with a tool_use nothing answers, and both the Anthropic and OpenAI
// APIs reject that outright — every tool_use must have a tool_result. So the
// session is not merely missing its last turn; it cannot be continued at all
// until each unanswered call is closed with an error result.
//
// It is written to run from persisted state rather than from what a loop held
// in memory, because the case it most needs to handle is the one where no loop
// is left to ask: the process died mid-stream. That makes it safe to call from
// anywhere — the error path, startup, and the resume request itself all reach
// the same fixed point, and calling it twice changes nothing the first call did
// not already.
func (lr *LoopRunner) ReconcileSession(sessionID session.SessionID) (*session.MessageInfo, error) {
	messages, err := lr.Store.GetMessages(sessionID, "", resumeScanLimit)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, nil
	}

	// Find the last assistant turn. Anything before it is settled.
	idx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == session.RoleAssistant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil
	}
	last := messages[idx]

	// Collect every call id already answered anywhere in the window, rather than
	// only in the messages listed after this turn.
	//
	// Ordering cannot carry the weight. Messages sort by their ULID, and the
	// generator draws non-monotonic entropy, so two ids minted in the same
	// millisecond sort in an arbitrary order — which is exactly what an
	// assistant turn and the tool-result message written straight after it are.
	// Reading position as causality would intermittently see a result as
	// preceding the call it answers, decide the call was unanswered, and append
	// a second result for it on every pass.
	//
	// A call id is unique to the call, so matching on the id needs no ordering
	// at all: a result exists or it does not.
	answered := map[string]bool{}
	for _, m := range messages {
		if m.Info.Role != session.RoleUser {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var data session.ToolPartData
			if json.Unmarshal(p.Data, &data) == nil && data.CallID != "" {
				answered[data.CallID] = true
			}
		}
	}

	var unanswered []session.ToolPartData
	for _, p := range last.Parts {
		if p.Type != session.PartTool {
			continue
		}
		var data session.ToolPartData
		if json.Unmarshal(p.Data, &data) != nil || data.CallID == "" || answered[data.CallID] {
			continue
		}
		// The call never got an answer. Whether it also never ran is a separate
		// question, and one worth asking: the commonest interruption is a tool
		// that finished and a loop that died before writing the paired result,
		// where the output is sitting right here and only the pairing is
		// missing.
		data.State = closedToolState(data.State)
		unanswered = append(unanswered, data)

		updated, _ := json.Marshal(data)
		part := p
		part.Data = updated
		part.UpdatedAt = session.Now()
		if err := lr.Store.UpdatePart(&part); err != nil {
			slog.Error("reconcile: update tool part", "session", sessionID, "err", err)
			continue
		}
		lr.Bus.Publish("message.part.updated", map[string]string{
			"sessionId": string(sessionID),
			"partId":    string(part.ID),
		})
	}

	if len(unanswered) > 0 {
		lr.writeInterruptedToolResults(sessionID, last.Info.ID, unanswered)
		slog.Info("reconciled dangling tool calls", "session", sessionID,
			"message", last.Info.ID, "calls", len(unanswered))
	}

	// Claim the turn if the model never finished it and nothing has claimed it
	// already.
	//
	// The condition is the finish reason, not its absence. An absent one means a
	// process that died before recording anything — but the far commoner shape
	// records "tool_calls" and stops: the model asked for a tool, the tool ran,
	// and the loop ended before pairing the result. Those sessions look idle
	// rather than broken, and until this claims them they offer no way forward,
	// because the unpaired call makes the next request invalid whoever sends it.
	//
	// Claiming is also what makes failures from before this existed visible. They
	// carry a finish reason and no interruption record, so keying on the reason
	// picks them up where keying on a missing field never would.
	if !session.FinishedNaturally(last.Info.Finish) && last.Info.Interrupted == nil {
		if last.Info.Finish == nil {
			last.Info.Interrupted = crashedInterruption()
			reason := "error"
			last.Info.Finish = &reason
		} else {
			last.Info.Interrupted = strandedInterruption(last.Info.Finish)
		}
		if err := lr.Store.UpdateMessage(&last.Info); err != nil {
			return nil, err
		}
		lr.Bus.Publish("message.updated", &last.Info)
	}

	if !last.Info.CanResume() {
		return nil, nil
	}
	return &last.Info, nil
}

// closedToolState turns a call that never finished into an error result,
// keeping whatever the tool had already reported about itself.
//
// The input is coerced when it will not parse. A stream cut mid-call leaves
// half-written JSON in the accumulated arguments, and that text is re-sent
// verbatim as the tool_use input on the next request — strict endpoints reject
// or stall on it. An empty object is wrong but valid, and the error result
// beside it tells the model not to read anything into it.
func closedToolState(state session.ToolState) session.ToolState {
	input := state.Input
	var obj map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &obj) != nil {
		input = json.RawMessage("{}")
	}

	// A call that reached a terminal status ran. Its output — or its own error —
	// is the true answer, and replacing that with "interrupted" would throw away
	// work the tool actually did and tell the model something false about it.
	// Only the pairing was missing, so only the pairing is added.
	if state.Status == session.ToolCompleted || state.Status == session.ToolError {
		state.Input = input
		return state
	}

	// Still pending or running when the loop went away: it did not finish, and
	// the model has to be told rather than left to assume.
	errStr := "Interrupted: the loop stopped before this tool call finished. It did not run to completion, so assume nothing about its effect — check the current state before repeating it."
	return session.ToolState{
		Status:   session.ToolError,
		Input:    input,
		Error:    &errStr,
		Title:    state.Title,
		Metadata: state.Metadata,
		Time:     state.Time,
	}
}

// writeInterruptedToolResults emits the one user message that answers every
// unfinished call, so no tool_use is left dangling.
func (lr *LoopRunner) writeInterruptedToolResults(sessionID session.SessionID, assistantID session.MessageID, calls []session.ToolPartData) {
	resultMsg := &session.MessageInfo{
		ID:        session.MessageID(id.NewMessageID()),
		SessionID: sessionID,
		Role:      session.RoleUser,
		ParentID:  &assistantID,
		CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(resultMsg); err != nil {
		slog.Error("reconcile: create tool-result message", "session", sessionID, "err", err)
		return
	}
	for _, c := range calls {
		data, _ := json.Marshal(c)
		part := &session.Part{
			ID:        session.PartID(id.NewPartID()),
			MessageID: resultMsg.ID,
			SessionID: sessionID,
			Type:      session.PartTool,
			Data:      data,
			CreatedAt: session.Now(),
			UpdatedAt: session.Now(),
		}
		if err := lr.Store.CreatePart(part); err != nil {
			slog.Error("reconcile: create tool-result part", "session", sessionID, "err", err)
		}
	}
	lr.Bus.Publish("message.updated", resultMsg)
}

// PrepareResume readies a session for the loop to be started again on it, and
// reports whether there was anything to resume.
//
// What it does with the interrupted turn depends on what the turn managed to
// produce, and the distinction is the whole design:
//
//   - A turn that made tool calls is kept. Some of those calls may have run to
//     completion before the loop died, and their results are on disk — real
//     work, sometimes with real side effects. Throwing the turn away would
//     throw those away too and invite the model to run them a second time,
//     which for a shell command or a write is not a repeat but a second effect.
//     ReconcileSession has already closed whichever calls went unanswered, so
//     the turn is consistent: the model sees the results it got, and an error
//     saying the rest never finished.
//
//   - A turn that produced only text is deleted. What survives is a fragment
//     that stops mid-word, and re-sending it asks the model to continue from
//     its own truncated output rather than to take the step again. Nothing is
//     lost by dropping it, because nothing outside the message was done.
//
// This is the same rule the loop already applies to a stream cancelled by
// mid-loop guidance, and for the same reasons.
//
// Neither branch needs to clear the finish reason. The loop only stops on a
// finished assistant turn when that turn is the last message, and after either
// branch it is not: a kept turn is followed by its tool results, and a deleted
// one leaves the user message before it.
func (lr *LoopRunner) PrepareResume(sessionID session.SessionID) (bool, error) {
	target, err := lr.ReconcileSession(sessionID)
	if err != nil {
		return false, err
	}
	if target == nil {
		return false, nil
	}

	msg, err := lr.Store.GetMessage(target.ID)
	if err != nil {
		return false, err
	}
	if msg == nil {
		// Already gone — the session is consistent and the loop can just run.
		return true, nil
	}

	hasToolCalls := false
	for _, p := range msg.Parts {
		if p.Type == session.PartTool {
			hasToolCalls = true
			break
		}
	}

	if hasToolCalls {
		slog.Info("resuming after interrupted turn; keeping its completed tool work",
			"session", sessionID, "message", target.ID, "reason", target.Interrupted.Reason)
		return true, nil
	}

	if err := lr.Store.DeleteMessage(target.ID); err != nil {
		return false, err
	}
	lr.Bus.Publish("message.deleted", map[string]string{
		"sessionId": string(sessionID),
		"messageId": string(target.ID),
	})
	slog.Info("resuming from a text-only interrupted turn; replaying the step",
		"session", sessionID, "message", target.ID, "reason", target.Interrupted.Reason)
	return true, nil
}
