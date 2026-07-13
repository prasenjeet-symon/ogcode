package session

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// DB returns the underlying database handle, used by helpers that operate on
// other tables (e.g. model capability records).
func (s *Store) DB() *db.DB { return s.db }

func (s *Store) Create(session *Session) error {
	_, err := s.db.Exec(
		`INSERT INTO session (id, project_id, directory, title, model, session_type, permission, compaction_summary, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.ProjectID, session.Directory, session.Title, session.Model, session.SessionType, session.Permission, session.CompactionSummary, session.CreatedAt, session.UpdatedAt,
	)
	return err
}

func (s *Store) Get(id SessionID) (*Session, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, directory, title, model, session_type, permission, compaction_summary, memory_tokens_saved, time_created, time_updated
		 FROM session WHERE id = ?`, id,
	)
	var sess Session
	err := row.Scan(&sess.ID, &sess.ProjectID, &sess.Directory, &sess.Title, &sess.Model, &sess.SessionType, &sess.Permission, &sess.CompactionSummary, &sess.MemoryTokensSaved, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

func (s *Store) List(directory string) ([]*Session, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, directory, title, model, session_type, permission, compaction_summary, memory_tokens_saved, time_created, time_updated
		 FROM session WHERE directory = ? AND session_type NOT IN ('note', 'index', 'search') ORDER BY time_updated DESC`, directory,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.Directory, &sess.Title, &sess.Model, &sess.SessionType, &sess.Permission, &sess.CompactionSummary, &sess.MemoryTokensSaved, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, &sess)
	}
	return sessions, nil
}

func (s *Store) Update(session *Session) error {
	_, err := s.db.Exec(
		`UPDATE session SET title = ?, model = ?, session_type = ?, permission = ?, compaction_summary = ?, time_updated = ? WHERE id = ?`,
		session.Title, session.Model, session.SessionType, session.Permission, session.CompactionSummary, session.UpdatedAt, session.ID,
	)
	return err
}

// UpdateMemoryTokensSaved atomically increments memory_tokens_saved by delta (may be negative).
// delta is clamped above at 1_000_000_000 to prevent overflow; negative values are
// preserved so callers can track memory overhead accurately.
func (s *Store) UpdateMemoryTokensSaved(id SessionID, delta int) error {
	// Clamp delta so the running total stays in a safe range.
	// MAX_TOKENS = 1 billion tokens ≈ 4 GB of text — far beyond any realistic session.
	const maxTokens = 1_000_000_000
	if delta > maxTokens {
		delta = maxTokens
	}
	_, err := s.db.Exec(
		`UPDATE session SET memory_tokens_saved = MIN(?, memory_tokens_saved + ?), time_updated = ? WHERE id = ?`,
		maxTokens, delta, Now(), id,
	)
	return err
}

// UpdateCompactionSummary updates only the compaction_summary column for a session,
// avoiding the race condition of overwriting other fields (e.g., title, model) that
// may have changed concurrently.
func (s *Store) UpdateCompactionSummary(id SessionID, summary string) error {
	_, err := s.db.Exec(
		`UPDATE session SET compaction_summary = ?, time_updated = ? WHERE id = ?`,
		summary, Now(), id,
	)
	return err
}

func (s *Store) Delete(id SessionID) error {
	_, err := s.db.Exec(`DELETE FROM session WHERE id = ?`, id)
	return err
}

// Message operations

func (s *Store) CreateMessage(msg *MessageInfo) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO message (id, session_id, data, time_created) VALUES (?, ?, ?, ?)`,
		msg.ID, msg.SessionID, string(data), msg.CreatedAt,
	)
	return err
}

func (s *Store) UpdateMessage(msg *MessageInfo) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE message SET data = ? WHERE id = ?`,
		string(data), msg.ID,
	)
	return err
}

// DeleteMessage removes a message and all of its parts. Foreign-key cascade
// handles part deletion automatically. Used to clean up partial assistant
// messages left behind when mid-loop guidance cancels a text-only stream —
// keeping them would produce two consecutive assistant role messages on the
// next prompt, which the Anthropic and OpenAI APIs reject with a 400.
func (s *Store) DeleteMessage(messageID MessageID) error {
	_, err := s.db.Exec(`DELETE FROM message WHERE id = ?`, messageID)
	return err
}

func (s *Store) GetMessages(sessionID SessionID, before MessageID, limit int) ([]*MessageWithParts, error) {
	var rows *sql.Rows
	var err error
	if before != "" {
		rows, err = s.db.Query(
			`SELECT id, session_id, data, time_created FROM message
			 WHERE session_id = ? AND id < ? ORDER BY id ASC LIMIT ?`,
			sessionID, before, limit,
		)
	} else {
		// Return the most recent N messages in ascending order.
		// Fetching DESC then re-sorting ASC in a subquery ensures the caller
		// always sees the latest messages, not the oldest N.
		rows, err = s.db.Query(
			`SELECT id, session_id, data, time_created FROM (
			   SELECT id, session_id, data, time_created FROM message
			   WHERE session_id = ? ORDER BY id DESC LIMIT ?
			 ) ORDER BY id ASC`,
			sessionID, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var result []*MessageWithParts
	for rows.Next() {
		var id, sessionID, data string
		var timeCreated int64
		if err := rows.Scan(&id, &sessionID, &data, &timeCreated); err != nil {
			return nil, err
		}
		var msg MessageInfo
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil, fmt.Errorf("unmarshal message: %w", err)
		}
		parts, err := s.GetParts(MessageID(id))
		if err != nil {
			return nil, err
		}
		result = append(result, &MessageWithParts{Info: msg, Parts: parts})
	}
	return result, nil
}

func (s *Store) GetMessage(messageID MessageID) (*MessageWithParts, error) {
	row := s.db.QueryRow(
		`SELECT id, session_id, data, time_created FROM message WHERE id = ?`, messageID,
	)
	var id, sessionID, data string
	var timeCreated int64
	if err := row.Scan(&id, &sessionID, &data, &timeCreated); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var msg MessageInfo
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return nil, err
	}
	parts, err := s.GetParts(messageID)
	if err != nil {
		return nil, err
	}
	return &MessageWithParts{Info: msg, Parts: parts}, nil
}

// Part operations — store the full Part JSON (including type) in the data column.

func (s *Store) CreatePart(part *Part) error {
	// Marshal the full Part into the data column so type is preserved
	data, err := json.Marshal(part)
	if err != nil {
		return fmt.Errorf("marshal part: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO part (id, message_id, session_id, data, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		part.ID, part.MessageID, part.SessionID, string(data), part.CreatedAt, part.UpdatedAt,
	)
	return err
}

func (s *Store) UpdatePart(part *Part) error {
	data, err := json.Marshal(part)
	if err != nil {
		return fmt.Errorf("marshal part: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE part SET data = ?, time_updated = ? WHERE id = ?`,
		string(data), part.UpdatedAt, part.ID,
	)
	return err
}

func (s *Store) GetParts(messageID MessageID) ([]Part, error) {
	rows, err := s.db.Query(
		`SELECT data FROM part WHERE message_id = ? ORDER BY id ASC`, messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []Part
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p Part
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, fmt.Errorf("unmarshal part: %w", err)
		}
		parts = append(parts, p)
	}
	return parts, nil
}

func (s *Store) GetPart(partID PartID) (*Part, error) {
	row := s.db.QueryRow(
		`SELECT data FROM part WHERE id = ?`, partID,
	)
	var data string
	err := row.Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Part
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, fmt.Errorf("unmarshal part: %w", err)
	}
	return &p, nil
}