package session

import "fmt"

// RecentTurnSummariesWindow is the default count of past turn summaries the
// next turn injects into its system prompt as continuity context. Two covers
// the immediate thread (e.g. plan → feedback) without dragging stale turns
// along; each summary is kept small (a few hundred tokens at most). The count
// a turn actually injects is read per turn from OGCODE_TURN_SUMMARY_WINDOW
// (see internal/agent), and falls back to this default when that variable is
// unset, empty, or unparseable.
const RecentTurnSummariesWindow = 2

// TurnSummary is one turn-local summary, persisted so it survives a restart.
// The Summary field is the LLM synthesis when generation succeeded and a
// head+tail truncation of the turn digest when it did not (see
// internal/agent's detached background writer) — either way the next turn
// reads it as "Turn N: <text>", oldest first.
type TurnSummary struct {
	ID        TurnSummaryID `json:"id"`
	SessionID SessionID     `json:"session_id"`
	// TurnIndex numbers turns within a session starting at 1. It is the max
	// existing index plus one, assigned atomically in InsertTurnSummary,
	// not a message-derived number, so a turn that failed to generate
	// still leaves a gap the reader can see when labelling ("Turn 3" then
	// "Turn 5").
	TurnIndex int    `json:"turn_index"`
	Summary   string `json:"summary"`
	UserText  string `json:"user_text,omitempty"`
	CreatedAt int64  `json:"time_created"`
}

// InsertTurnSummary persists one turn-local summary for sessionID and returns
// it with its assigned ID, turn index and timestamp. A nil store, an unknown
// session, or an empty summary stores nothing and returns an error (the
// caller treats a failed summary as a skipped summary, never a failed turn).
func (s *Store) InsertTurnSummary(sessionID SessionID, summary string, userText string) (*TurnSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("insert turn summary: no store")
	}
	if summary == "" {
		return nil, fmt.Errorf("insert turn summary: empty summary")
	}
	// Number the turn by the session's max existing index plus one in a
	// single statement, so concurrent inserts on the same session cannot
	// read the same max and collide on turn_index (UNIQUE(session_id,
	// turn_index) rejects a duplicate). The index is stable within the
	// session and the reader can label "Turn N" without counting messages.
	ts := &TurnSummary{
		ID:        NewTurnSummaryID(),
		SessionID: sessionID,
		Summary:   summary,
		UserText:  userText,
		CreatedAt: Now(),
	}
	if _, err := s.db.Exec(
		`INSERT INTO turn_summary (id, session_id, turn_index, summary, user_text, time_created)
		 SELECT ?, ?, COALESCE(MAX(turn_index), 0) + 1, ?, ?, ?
		 FROM turn_summary
		 WHERE session_id = ?`,
		ts.ID, ts.SessionID, ts.Summary, ts.UserText, ts.CreatedAt, sessionID,
	); err != nil {
		return nil, fmt.Errorf("insert turn summary: %w", err)
	}
	if err := s.db.QueryRow(`SELECT turn_index FROM turn_summary WHERE id = ?`, ts.ID).Scan(&ts.TurnIndex); err != nil {
		return nil, fmt.Errorf("insert turn summary: read index: %w", err)
	}
	return ts, nil
}

// RecentTurnSummaries returns up to limit of the most recent turn summaries
// for sessionID, oldest first — the order the next turn injects them so the
// block reads as a short narrative. A nil store or an unknown session yields
// nothing, never an error, so a caller without a persisted summary simply
// injects no entry.
func (s *Store) RecentTurnSummaries(sessionID SessionID, limit int) ([]*TurnSummary, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, session_id, turn_index, summary, user_text, time_created
		   FROM turn_summary
		  WHERE session_id = ?
		  ORDER BY turn_index DESC
		  LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent turn summaries: %w", err)
	}
	defer rows.Close()

	var desc []*TurnSummary
	for rows.Next() {
		ts := &TurnSummary{}
		if err := rows.Scan(&ts.ID, &ts.SessionID, &ts.TurnIndex, &ts.Summary, &ts.UserText, &ts.CreatedAt); err != nil {
			return nil, fmt.Errorf("recent turn summaries: scan: %w", err)
		}
		desc = append(desc, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent turn summaries: %w", err)
	}
	// Reverse DESC into chronological order for injection.
	out := make([]*TurnSummary, len(desc))
	for i, ts := range desc {
		out[len(desc)-1-i] = ts
	}
	return out, nil
}
