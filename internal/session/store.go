package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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
		`INSERT INTO session (id, project_id, directory, title, model, provider, session_type, permission, compaction_summary, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.ProjectID, session.Directory, session.Title, session.Model, session.Provider, session.SessionType, session.Permission, session.CompactionSummary, session.CreatedAt, session.UpdatedAt,
	)
	return err
}

func (s *Store) Get(id SessionID) (*Session, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, directory, title, model, provider, session_type, permission, compaction_summary,
		        utility_input, utility_output, utility_reasoning, utility_cache_read, utility_cache_write,
		        time_created, time_updated
		 FROM session WHERE id = ?`, id,
	)
	var sess Session
	var uIn, uOut, uReason, uCacheRead, uCacheWrite int
	err := row.Scan(&sess.ID, &sess.ProjectID, &sess.Directory, &sess.Title, &sess.Model, &sess.Provider, &sess.SessionType, &sess.Permission, &sess.CompactionSummary,
		&uIn, &uOut, &uReason, &uCacheRead, &uCacheWrite, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.UtilityTokens = utilityTokens(uIn, uOut, uReason, uCacheRead, uCacheWrite)
	return &sess, nil
}

func (s *Store) List(directory string) ([]*Session, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, directory, title, model, provider, session_type, permission, compaction_summary,
		        utility_input, utility_output, utility_reasoning, utility_cache_read, utility_cache_write,
		        time_created, time_updated
		 FROM session WHERE directory = ? AND session_type NOT IN ('note', 'index', 'search') ORDER BY time_updated DESC`, directory,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var sess Session
		var uIn, uOut, uReason, uCacheRead, uCacheWrite int
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.Directory, &sess.Title, &sess.Model, &sess.Provider, &sess.SessionType, &sess.Permission, &sess.CompactionSummary,
			&uIn, &uOut, &uReason, &uCacheRead, &uCacheWrite, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sess.UtilityTokens = utilityTokens(uIn, uOut, uReason, uCacheRead, uCacheWrite)
		sessions = append(sessions, &sess)
	}
	return sessions, nil
}

// ListAll returns every session row regardless of directory or type, including
// the note/index/search sessions List hides. Agentic memory uses it to backfill
// project identity onto nodes written before that column existed.
func (s *Store) ListAll() ([]*Session, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, directory, title, model, provider, session_type, permission, compaction_summary,
		        utility_input, utility_output, utility_reasoning, utility_cache_read, utility_cache_write,
		        time_created, time_updated
		 FROM session ORDER BY time_updated DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var sess Session
		var uIn, uOut, uReason, uCacheRead, uCacheWrite int
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.Directory, &sess.Title, &sess.Model, &sess.Provider, &sess.SessionType, &sess.Permission, &sess.CompactionSummary,
			&uIn, &uOut, &uReason, &uCacheRead, &uCacheWrite, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sess.UtilityTokens = utilityTokens(uIn, uOut, uReason, uCacheRead, uCacheWrite)
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

// Update rewrites the mutable, user-visible columns of a session. It
// deliberately leaves the utility_* columns alone: those are written only by
// AddUtilityUsage, which increments them, so a read-modify-write here would
// clobber an increment that landed between the caller's Get and this Update.
func (s *Store) Update(session *Session) error {
	_, err := s.db.Exec(
		`UPDATE session SET title = ?, model = ?, provider = ?, session_type = ?, permission = ?, compaction_summary = ?, time_updated = ? WHERE id = ?`,
		session.Title, session.Model, session.Provider, session.SessionType, session.Permission, session.CompactionSummary, session.UpdatedAt, session.ID,
	)
	return err
}

// AddUtilityUsage adds the tokens a utility call spent to the session's running
// totals. Utility calls — title generation, command risk assessment, context
// compaction — stream their usage on a call the main-turn accounting never
// sees, so it accumulates here instead of on a message. The UPDATE increments
// rather than assigns, so concurrent calls cannot lose one another's tokens, and
// it leaves time_updated untouched: this is not a user-visible session change.
func (s *Store) AddUtilityUsage(id SessionID, u TokenCounts) error {
	if u.Input == 0 && u.Output == 0 && u.Reasoning == 0 && u.CacheRead == 0 && u.CacheWrite == 0 {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE session SET
		        utility_input = utility_input + ?,
		        utility_output = utility_output + ?,
		        utility_reasoning = utility_reasoning + ?,
		        utility_cache_read = utility_cache_read + ?,
		        utility_cache_write = utility_cache_write + ?
		 WHERE id = ?`,
		u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite, id,
	)
	return err
}

// utilityTokens rebuilds the utility TokenCounts from the five stored columns,
// returning nil while every count is zero so an untouched session marshals
// without the field.
func utilityTokens(input, output, reasoning, cacheRead, cacheWrite int) *TokenCounts {
	if input == 0 && output == 0 && reasoning == 0 && cacheRead == 0 && cacheWrite == 0 {
		return nil
	}
	tc := &TokenCounts{
		Input:      input,
		Output:     output,
		Reasoning:  reasoning,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
	}
	tc.Total = tc.Consumed()
	return tc
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

// StepUsage is what one assistant step spent and on which endpoint: the part of
// a message a cost view needs, read without the message's parts.
type StepUsage struct {
	MessageID MessageID
	SessionID SessionID
	Model     string
	Provider  string
	Tokens    TokenCounts
	CreatedAt int64
}

// ListStepUsage returns every assistant step that reported usage, oldest first —
// one session's when sessionID is set, the whole project's when it is empty. It
// decodes only the message rows, never their parts, so pricing a long session
// does not load its transcript.
func (s *Store) ListStepUsage(sessionID SessionID) ([]StepUsage, error) {
	query := `SELECT data FROM message`
	var args []any
	if sessionID != "" {
		query += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY time_created, id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list step usage: %w", err)
	}
	defer rows.Close()

	var out []StepUsage
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var msg MessageInfo
		// One unreadable row must not hide the rest of a project's spend.
		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}
		if msg.Role != RoleAssistant || msg.Tokens == nil || msg.Tokens.Consumed() == 0 {
			continue
		}
		out = append(out, StepUsage{
			MessageID: msg.ID,
			SessionID: msg.SessionID,
			Model:     msg.Model,
			Provider:  msg.Provider,
			Tokens:    *msg.Tokens,
			CreatedAt: msg.CreatedAt,
		})
	}
	return out, rows.Err()
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
		// Backward pagination: return the most recent `limit` messages
		// strictly older than the cursor, ascending. Like the first-page
		// branch, fetch DESC then re-sort ASC in a subquery — selecting
		// ASC LIMIT directly would return the OLDEST `limit` below the
		// cursor and leave a gap between pages.
		rows, err = s.db.Query(
			`SELECT id, session_id, data, time_created FROM (
			   SELECT id, session_id, data, time_created FROM message
			   WHERE session_id = ? AND id < ? ORDER BY id DESC LIMIT ?
			 ) ORDER BY id ASC`,
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
	var ids []MessageID
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
		result = append(result, &MessageWithParts{Info: msg})
		ids = append(ids, MessageID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Parts for the whole page in one query. The per-message loop this replaced
	// issued one SELECT per message — 300 round trips for a full transcript.
	partsByMessage, err := s.GetPartsForMessages(ids)
	if err != nil {
		return nil, err
	}
	for i, m := range result {
		// A message with no parts (an errored or aborted turn) is sent as
		// "parts": [] rather than null, so no client has to guard every read.
		if parts := partsByMessage[ids[i]]; parts != nil {
			m.Parts = parts
		} else {
			m.Parts = []Part{}
		}
	}
	return result, nil
}

// GetMessagesPage is GetMessages for a paginating reader: the newest `limit`
// messages older than `before` (or overall, when before is empty), oldest
// first, plus whether anything older remains. It reads one row past the page
// rather than issuing a second query — the extra row's presence is the answer,
// and it is dropped before returning — so a page that happens to end exactly
// at the first message still reports that nothing is left.
func (s *Store) GetMessagesPage(sessionID SessionID, before MessageID, limit int) ([]*MessageWithParts, bool, error) {
	msgs, err := s.GetMessages(sessionID, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(msgs) > limit {
		// Ascending order, so the surplus row is the oldest one.
		return msgs[len(msgs)-limit:], true, nil
	}
	return msgs, false, nil
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

// GetPartsForMessages loads the parts of many messages in a single query,
// grouped by message id and preserving each message's creation order. The ids
// are chunked so a long page never exceeds SQLite's bound-parameter limit
// (SQLITE_MAX_VARIABLE_NUMBER, 999 on older builds).
func (s *Store) GetPartsForMessages(ids []MessageID) (map[MessageID][]Part, error) {
	out := make(map[MessageID][]Part, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		end := start + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = string(id)
		}
		rows, err := s.db.Query(
			`SELECT message_id, data FROM part WHERE message_id IN (`+placeholders+`) ORDER BY message_id ASC, id ASC`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var messageID, data string
			if err := rows.Scan(&messageID, &data); err != nil {
				rows.Close()
				return nil, err
			}
			var p Part
			if err := json.Unmarshal([]byte(data), &p); err != nil {
				rows.Close()
				return nil, fmt.Errorf("unmarshal part: %w", err)
			}
			id := MessageID(messageID)
			out[id] = append(out[id], p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
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
