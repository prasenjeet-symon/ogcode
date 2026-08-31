package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// NodeType distinguishes the three levels in the hierarchy.
type NodeType string

const (
	TypeTopic   NodeType = "topic"
	TypeConcept NodeType = "concept"
	TypeFact    NodeType = "fact"
)

// Node is the fundamental unit in the knowledge graph.
//
// ProjectID and SessionType are denormalized copies of the owning session's
// project directory and type. They are stamped at write time so project-scoped
// recall can filter without joining against the per-project session database
// (which lives in a different file and, for worktree sessions, keys sessions by
// a directory the memory store never sees).
type Node struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"sessionId"`
	ProjectID  string    `json:"projectId,omitempty"`
	SessionType string   `json:"sessionType,omitempty"`
	Type       NodeType  `json:"type"`
	Key        string    `json:"key"`
	Content    string    `json:"content,omitempty"`    // question + " [ANSWER] " + response for facts
	Question   string    `json:"question,omitempty"`   // original question
	Response   string    `json:"response,omitempty"`   // original answer
	TopicName  string    `json:"topicName,omitempty"` // only set for concept/fact nodes
	Summary    string    `json:"summary,omitempty"`    // LLM-generated one-line summary per fact
	Labels     []string  `json:"labels,omitempty"`    // LLM-generated labels per fact
	Order      int       `json:"order,omitempty"`     // position in conversation (1-indexed)
	CreatedAt  int64     `json:"createdAt"`
	AccessedAt int64     `json:"accessedAt"`
}

// Edge represents a named relationship between two nodes.
type Edge struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"sessionId"`
	FromKey   string    `json:"fromKey"`   // concept key
	ToKey     string    `json:"toKey"`     // concept key (cross-topic allowed)
	RelType   string    `json:"relType"`   // e.g. "related", "prerequisite", "opposite"
	Weight    float32   `json:"weight"`
	CreatedAt int64     `json:"createdAt"`
}

// SessionMeta is the metadata for a session.
type SessionMeta struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId,omitempty"`
	SessionType  string `json:"sessionType,omitempty"`
	Name         string `json:"name,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	LastAccessAt int64  `json:"lastAccessAt"`
	NodeCount    int    `json:"nodeCount"`
	TopicCount   int    `json:"topicCount"`
	ConceptCount int    `json:"conceptCount"`
	FactCount    int    `json:"factCount"`
}

// Store is the SQLite-backed knowledge graph. All methods are
// safe for concurrent use; writes are serialized through a mutex.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// Open opens (or creates) the memory database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("memory: mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("memory: open: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying sql.DB for advanced queries.
func (s *Store) DB() *sql.DB {
	return s.db
}

// ──── Session ────

// SessionUpsert carries the session attributes a write path knows about.
// Empty fields are ignored on update, so a caller that only knows the session
// ID cannot blank out a project association recorded earlier.
type SessionUpsert struct {
	ID          string
	ProjectID   string
	SessionType string
	Name        string
}

// EnsureSession creates a session if it does not exist, and updates lastAccessAt.
// Read paths use this when the session's project is unknown or irrelevant.
func (s *Store) EnsureSession(sessionID string) error {
	return s.EnsureSessionMeta(SessionUpsert{ID: sessionID})
}

// EnsureSessionMeta upserts a session along with whatever attributes the caller
// knows. Only non-empty attributes overwrite existing values.
func (s *Store) EnsureSessionMeta(u SessionUpsert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, project_id, session_type, name, created_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			last_accessed_at = excluded.last_accessed_at,
			project_id   = CASE WHEN excluded.project_id   = '' THEN sessions.project_id   ELSE excluded.project_id   END,
			session_type = CASE WHEN excluded.session_type = '' THEN sessions.session_type ELSE excluded.session_type END,
			name         = CASE WHEN excluded.name         = '' THEN sessions.name         ELSE excluded.name         END
	`, u.ID, u.ProjectID, u.SessionType, u.Name, now, now)
	return err
}

// DeleteSession removes a session and all its data (cascading via FK triggers if present).
func (s *Store) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID)
	// Also clean up orphaned nodes and edges.
	_, _ = s.db.Exec(`DELETE FROM nodes WHERE session_id = ?`, sessionID)
	_, _ = s.db.Exec(`DELETE FROM edges WHERE session_id = ?`, sessionID)
	return err
}

// ListSessions returns all sessions ordered by last access.
func (s *Store) ListSessions() ([]SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
		SELECT s.id, s.project_id, s.session_type, s.name, s.created_at, s.last_accessed_at,
			   COUNT(n.id) as node_count,
			   SUM(CASE WHEN n.type = 'topic' THEN 1 ELSE 0 END) as topic_count,
			   SUM(CASE WHEN n.type = 'concept' THEN 1 ELSE 0 END) as concept_count,
			   SUM(CASE WHEN n.type = 'fact' THEN 1 ELSE 0 END) as fact_count
		FROM sessions s
		LEFT JOIN nodes n ON n.session_id = s.id
		GROUP BY s.id
		ORDER BY s.last_accessed_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var m SessionMeta
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.SessionType, &m.Name, &m.CreatedAt, &m.LastAccessAt,
			&m.NodeCount, &m.TopicCount, &m.ConceptCount, &m.FactCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ──── Node CRUD ────

// AddNode inserts a node. If a node with the same (session_id, key) exists, it is updated (upsert).
// Returns the node with its ID set.
func (s *Store) AddNode(n Node) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	labels, _ := json.Marshal(n.Labels)

	order := n.Order
	if n.Type == TypeFact && order == 0 {
		row := s.db.QueryRow(`SELECT COALESCE(MAX("order"), 0) FROM nodes WHERE session_id = ? AND type = 'fact'`, n.SessionID)
		_ = row.Scan(&order)
		order++
	}

	_, err := s.db.Exec(`
		INSERT INTO nodes (session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, key) DO UPDATE SET
			content = excluded.content,
			question = excluded.question,
			response = excluded.response,
			topic_name = excluded.topic_name,
			summary = excluded.summary,
			labels = excluded.labels,
			"order" = excluded."order",
			accessed_at = excluded.accessed_at,
			project_id   = CASE WHEN excluded.project_id   = '' THEN nodes.project_id   ELSE excluded.project_id   END,
			session_type = CASE WHEN excluded.session_type = '' THEN nodes.session_type ELSE excluded.session_type END
	`, n.SessionID, n.ProjectID, n.SessionType, string(n.Type), n.Key, n.Content, n.Question, n.Response, n.TopicName, n.Summary, string(labels), order, now, now)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRow(`SELECT id FROM nodes WHERE session_id = ? AND key = ?`, n.SessionID, n.Key)
	var id int64
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	n.ID = id
	n.Order = order
	n.CreatedAt = now
	n.AccessedAt = now
	return &n, nil
}

// GetNode retrieves a node by session_id + key.
func (s *Store) GetNode(sessionID, key string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
		FROM nodes WHERE session_id = ? AND key = ?
	`, sessionID, key)
	return s.scanNode(row)
}

// DeleteNode removes a node by session_id + key.
func (s *Store) DeleteNode(sessionID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM nodes WHERE session_id = ? AND key = ?`, sessionID, key)
	return err
}

// ListNodes returns all nodes for a session, optionally filtered by type.
func (s *Store) ListNodes(sessionID string, filterType NodeType) ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var query string
	var args []any
	if filterType != "" {
		query = `SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
				 FROM nodes WHERE session_id = ? AND type = ? ORDER BY topic_name, id`
		args = []any{sessionID, string(filterType)}
	} else {
		query = `SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
				 FROM nodes WHERE session_id = ? ORDER BY topic_name, id`
		args = []any{sessionID}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := s.scanNodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// NodeFilter captures all optional bounds on a node query.
type NodeFilter struct {
	Type      NodeType
	Since     int64  // unix milliseconds; 0 = no lower bound
	Until     int64  // unix milliseconds; 0 = no upper bound
	FromOrder int    // 1-indexed inclusive; 0 = no lower bound
	ToOrder   int    // 1-indexed inclusive; 0 = no upper bound
	TopicName string // empty = any topic
}

// ListNodesFiltered returns nodes matching the given bounds.
func (s *Store) ListNodesFiltered(sessionID string, f NodeFilter) ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clause := "session_id = ?"
	args := []any{sessionID}
	if f.Type != "" {
		clause += " AND type = ?"
		args = append(args, string(f.Type))
	}
	if f.Since > 0 {
		clause += " AND created_at >= ?"
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		clause += " AND created_at <= ?"
		args = append(args, f.Until)
	}
	if f.FromOrder > 0 {
		clause += ` AND "order" >= ?`
		args = append(args, f.FromOrder)
	}
	if f.ToOrder > 0 {
		clause += ` AND "order" <= ?`
		args = append(args, f.ToOrder)
	}
	if f.TopicName != "" {
		clause += " AND topic_name = ?"
		args = append(args, f.TopicName)
	}
	query := fmt.Sprintf(`SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
						 FROM nodes WHERE %s ORDER BY topic_name, "order", id`, clause)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := s.scanNodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// GetNodeAt returns the Nth fact (1-indexed) in a session.
func (s *Store) GetNodeAt(sessionID string, order int) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
		FROM nodes WHERE session_id = ? AND type = 'fact' AND "order" = ? ORDER BY id
		`, sessionID, order)
	return s.scanNode(row)
}

func (s *Store) GetFirstFact(sessionID string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
		FROM nodes WHERE session_id = ? AND type = 'fact' ORDER BY "order", id LIMIT 1
		`, sessionID)
	return s.scanNode(row)
}

func (s *Store) GetLastFact(sessionID string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
		FROM nodes WHERE session_id = ? AND type = 'fact' ORDER BY "order" DESC, id DESC LIMIT 1
		`, sessionID)
	return s.scanNode(row)
}

// AddEdge creates a relationship between two concepts.
func (s *Store) AddEdge(e Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`
		INSERT INTO edges (session_id, from_key, to_key, rel_type, weight, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.SessionID, e.FromKey, e.ToKey, e.RelType, e.Weight, now)
	return err
}

// ListEdges returns all edges for a session.
func (s *Store) ListEdges(sessionID string) ([]Edge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
		SELECT id, session_id, from_key, to_key, rel_type, weight, created_at
		FROM edges WHERE session_id = ? ORDER BY created_at
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.SessionID, &e.FromKey, &e.ToKey, &e.RelType, &e.Weight, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEdge removes an edge by from_key + to_key.
func (s *Store) DeleteEdge(sessionID, fromKey, toKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM edges WHERE session_id = ? AND from_key = ? AND to_key = ?`,
		sessionID, fromKey, toKey)
	return err
}

// UpdateNodeEnrichment updates labels and summary for a fact node.
func (s *Store) UpdateNodeEnrichment(sessionID, key, summary string, labels []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lbytes, _ := json.Marshal(labels)
	_, err := s.db.Exec(`UPDATE nodes SET summary = ?, labels = ? WHERE session_id = ? AND key = ?`,
		summary, string(lbytes), sessionID, key)
	return err
}

// Embeddings returns all (key, embedding) pairs for a session.
func (s *Store) Embeddings(sessionID string) (map[string][]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT key, embedding FROM nodes WHERE session_id = ? AND embedding IS NOT NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string][]float32)
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var emb []float32
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &emb)
		}
		m[key] = emb
	}
	return m, rows.Err()
}

// SetEmbedding upserts the embedding for a node key.
func (s *Store) SetEmbedding(sessionID, key string, emb []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(emb)
	_, err := s.db.Exec(`UPDATE nodes SET embedding = ? WHERE session_id = ? AND key = ?`, raw, sessionID, key)
	return err
}

// Stats returns per-type counts for a session.
func (s *Store) Stats(sessionID string) (topics, concepts, facts int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT
			SUM(CASE WHEN type = 'topic' THEN 1 ELSE 0 END),
			SUM(CASE WHEN type = 'concept' THEN 1 ELSE 0 END),
			SUM(CASE WHEN type = 'fact' THEN 1 ELSE 0 END)
		FROM nodes WHERE session_id = ?
	`, sessionID)
	err = row.Scan(&topics, &concepts, &facts)
	return
}

// ──── Project-scoped access ────

// ProjectFilter bounds a project-scoped query. Session-scoped NodeFilter cannot
// be reused because project queries span many sessions, where the per-session
// "order" column is not comparable across rows.
type ProjectFilter struct {
	Since        int64    // unix milliseconds; 0 = no lower bound
	Until        int64    // unix milliseconds; 0 = no upper bound
	SessionTypes []string // empty = any session type
	TopicName    string   // empty = any topic
	ExcludeSession string // session ID to skip (usually the caller's own)
	// OnlySession restricts the query to a single conversation. It is how the
	// project pipeline — dated, attributed, recency-ranked — gets pointed at just
	// the current session. It takes precedence over SessionTypes: the caller
	// named the session explicitly, so its type is not a reason to skip it.
	OnlySession string
}

// clause renders the shared WHERE fragment for project queries.
func (f ProjectFilter) clause(projectID string, nodeType NodeType) (string, []any) {
	clause := "project_id = ?"
	args := []any{projectID}
	if nodeType != "" {
		clause += " AND type = ?"
		args = append(args, string(nodeType))
	}
	if f.Since > 0 {
		clause += " AND created_at >= ?"
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		clause += " AND created_at <= ?"
		args = append(args, f.Until)
	}
	if f.TopicName != "" {
		clause += " AND topic_name = ?"
		args = append(args, f.TopicName)
	}
	if f.ExcludeSession != "" {
		clause += " AND session_id != ?"
		args = append(args, f.ExcludeSession)
	}
	if f.OnlySession != "" {
		clause += " AND session_id = ?"
		args = append(args, f.OnlySession)
	}
	if len(f.SessionTypes) > 0 && f.OnlySession == "" {
		// Rows written before session_type existed carry '', and excluding them
		// would hide a user's entire pre-upgrade history. They are conversation
		// turns by construction (subagent memory writes are a newer feature), so
		// the unknown value is treated as included.
		clause += " AND (session_type = '' OR session_type IN (" + placeholders(len(f.SessionTypes)) + "))"
		for _, t := range f.SessionTypes {
			args = append(args, t)
		}
	}
	return clause, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ScanProjectFacts streams every fact in a project through fn along with its
// decoded embedding (nil when the fact was never embedded).
//
// This is a callback scan rather than a slice return because a project graph can
// hold orders of magnitude more facts than a session graph: the caller keeps a
// bounded top-K and per-topic tallies instead of materializing every row.
func (s *Store) ScanProjectFacts(projectID string, f ProjectFilter, fn func(Node, []float32)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clause, args := f.clause(projectID, TypeFact)
	rows, err := s.db.Query(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at, embedding
		FROM nodes WHERE `+clause, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n Node
		var labels, embRaw []byte
		var content, question, response, topicName, summary sql.NullString
		var projID, sessType sql.NullString
		if err := rows.Scan(&n.ID, &n.SessionID, &projID, &sessType, &n.Type, &n.Key,
			&content, &question, &response, &topicName, &summary, &labels,
			&n.Order, &n.CreatedAt, &n.AccessedAt, &embRaw); err != nil {
			return err
		}
		n.ProjectID = projID.String
		n.SessionType = sessType.String
		n.Content = content.String
		n.Question = question.String
		n.Response = response.String
		n.TopicName = topicName.String
		n.Summary = summary.String
		if len(labels) > 0 {
			_ = json.Unmarshal(labels, &n.Labels)
		}
		var emb []float32
		if len(embRaw) > 0 {
			_ = json.Unmarshal(embRaw, &emb)
		}
		fn(n, emb)
	}
	return rows.Err()
}

// ProjectFactsAt returns the facts at the given per-session orders. Project
// recall uses it for neighbour windowing, where "the fact before this one" is
// only meaningful within the same session.
//
// The caller's ProjectFilter is applied here too: a neighbour is still a fact
// the user asked to exclude, so a date range or topic restriction must not be
// escaped by being adjacent to something that matched.
func (s *Store) ProjectFactsAt(projectID, sessionID string, orders []int, f ProjectFilter) ([]Node, error) {
	if len(orders) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clause, args := f.clause(projectID, TypeFact)
	clause += ` AND session_id = ? AND "order" IN (` + placeholders(len(orders)) + `)`
	args = append(args, sessionID)
	for _, o := range orders {
		args = append(args, o)
	}
	rows, err := s.db.Query(`
		SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
		FROM nodes WHERE `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := s.scanNodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// ListProjectConcepts returns the concept nodes across a project, most recent
// first. They give recall a vocabulary for follow-up searches.
func (s *Store) ListProjectConcepts(projectID string, f ProjectFilter, limit int) ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clause, args := f.clause(projectID, TypeConcept)
	query := `SELECT id, session_id, project_id, session_type, type, key, content, question, response, topic_name, summary, labels, "order", created_at, accessed_at
			  FROM nodes WHERE ` + clause + ` ORDER BY created_at DESC`
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := s.scanNodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// TopicCount is a topic name with how many facts sit under it.
type TopicCount struct {
	Name  string
	Facts int
}

// ListProjectTopics returns a project's topics ordered by size. Fact placement
// uses it to reuse topic names another session already established, so one
// project does not end up with "Auth System", "Authentication" and "auth" as
// three unrelated topics.
func (s *Store) ListProjectTopics(projectID string, f ProjectFilter, limit int) ([]TopicCount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clause, args := f.clause(projectID, TypeFact)
	query := `SELECT topic_name, COUNT(*) AS c FROM nodes WHERE ` + clause +
		` AND topic_name != '' GROUP BY topic_name ORDER BY c DESC, topic_name`
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopicCount
	for rows.Next() {
		var t TopicCount
		if err := rows.Scan(&t.Name, &t.Facts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ProjectSessionNames returns sessionID → name for a project, used to label
// recalled facts with the conversation they came from.
func (s *Store) ProjectSessionNames(projectID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, name FROM sessions WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ProjectStats returns per-project counts.
func (s *Store) ProjectStats(projectID string) (sessions, topics, facts int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
		SELECT
			COUNT(DISTINCT session_id),
			COUNT(DISTINCT CASE WHEN type = 'fact' THEN topic_name END),
			SUM(CASE WHEN type = 'fact' THEN 1 ELSE 0 END)
		FROM nodes WHERE project_id = ?
	`, projectID)
	var f sql.NullInt64
	if err = row.Scan(&sessions, &topics, &f); err != nil {
		return
	}
	facts = int(f.Int64)
	return
}

// BackfillProject stamps project_id/session_type onto rows written before those
// columns existed. sessionTypes maps session ID → session type for every session
// belonging to the project; only rows whose project_id is still empty are
// touched, so this is idempotent and never re-homes a session.
func (s *Store) BackfillProject(projectID string, sessionTypes map[string]string) (int64, error) {
	if projectID == "" || len(sessionTypes) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var total int64
	for sessionID, sessionType := range sessionTypes {
		res, err := tx.Exec(
			`UPDATE nodes SET project_id = ?, session_type = ? WHERE session_id = ? AND project_id = ''`,
			projectID, sessionType, sessionID)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if _, err := tx.Exec(
			`UPDATE sessions SET project_id = ?, session_type = ? WHERE id = ? AND project_id = ''`,
			projectID, sessionType, sessionID); err != nil {
			return total, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

// ──── Internal helpers ────

func (s *Store) scanNode(row *sql.Row) (*Node, error) {
	var n Node
	var labels []byte
	var content, question, response, topicName, summary sql.NullString
	var projectID, sessionType sql.NullString
	if err := row.Scan(&n.ID, &n.SessionID, &projectID, &sessionType, &n.Type, &n.Key, &content, &question, &response, &topicName, &summary, &labels, &n.Order, &n.CreatedAt, &n.AccessedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	n.ProjectID = projectID.String
	n.SessionType = sessionType.String
	n.Content = content.String
	n.Question = question.String
	n.Response = response.String
	n.TopicName = topicName.String
	n.Summary = summary.String
	if len(labels) > 0 {
		_ = json.Unmarshal(labels, &n.Labels)
	}
	return &n, nil
}

func (s *Store) scanNodeRows(rows *sql.Rows) (*Node, error) {
	var n Node
	var labels []byte
	var content, question, response, topicName, summary sql.NullString
	var projectID, sessionType sql.NullString
	if err := rows.Scan(&n.ID, &n.SessionID, &projectID, &sessionType, &n.Type, &n.Key, &content, &question, &response, &topicName, &summary, &labels, &n.Order, &n.CreatedAt, &n.AccessedAt); err != nil {
		return nil, err
	}
	n.ProjectID = projectID.String
	n.SessionType = sessionType.String
	n.Content = content.String
	n.Question = question.String
	n.Response = response.String
	n.TopicName = topicName.String
	n.Summary = summary.String
	if len(labels) > 0 {
		_ = json.Unmarshal(labels, &n.Labels)
	}
	return &n, nil
}

var errNotFound = errors.New("memory: not found")

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS sessions (
		id                  TEXT PRIMARY KEY,
		project_id          TEXT NOT NULL DEFAULT '',
		session_type        TEXT NOT NULL DEFAULT '',
		name                TEXT NOT NULL DEFAULT '',
		created_at          INTEGER NOT NULL,
		last_accessed_at    INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS nodes (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		project_id   TEXT NOT NULL DEFAULT '',
		session_type TEXT NOT NULL DEFAULT '',
		type        TEXT NOT NULL,
		key         TEXT NOT NULL,
		content     TEXT NOT NULL DEFAULT '',
		question    TEXT NOT NULL DEFAULT '',
		response    TEXT NOT NULL DEFAULT '',
		topic_name  TEXT NOT NULL DEFAULT '',
		embedding   BLOB,
		summary     TEXT NOT NULL DEFAULT '',
		labels      TEXT NOT NULL DEFAULT '[]',
		"order"     INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL,
		accessed_at INTEGER NOT NULL,
		UNIQUE(session_id, key)
	);
	CREATE INDEX IF NOT EXISTS idx_nodes_session ON nodes(session_id);
	CREATE INDEX IF NOT EXISTS idx_nodes_session_type ON nodes(session_id, type);
	CREATE INDEX IF NOT EXISTS idx_nodes_topic ON nodes(session_id, topic_name);
	CREATE INDEX IF NOT EXISTS idx_nodes_order ON nodes(session_id, "order");
	CREATE INDEX IF NOT EXISTS idx_nodes_created ON nodes(session_id, created_at);

	CREATE TABLE IF NOT EXISTS edges (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		from_key    TEXT NOT NULL,
		to_key      TEXT NOT NULL,
		rel_type    TEXT NOT NULL DEFAULT 'related',
		weight      REAL NOT NULL DEFAULT 1.0,
		created_at  INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_edges_session ON edges(session_id);

	CREATE TABLE IF NOT EXISTS memory_collection (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL UNIQUE,
		created_at  INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS memory_document (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		collection  TEXT NOT NULL DEFAULT 'default',
		content     TEXT NOT NULL,
		embedding   BLOB,
		created_at  INTEGER NOT NULL,
		UNIQUE(collection, content)
	);
	CREATE INDEX IF NOT EXISTS idx_memdoc_collection ON memory_document(collection);
	`)
	if err != nil {
		return err
	}

	// Databases created before project-scoped recall existed already have the
	// tables above, so CREATE TABLE IF NOT EXISTS is a no-op for them and the
	// new columns must be added explicitly. SQLite has no ADD COLUMN IF NOT
	// EXISTS, and re-running ALTER on an up-to-date DB errors with "duplicate
	// column name" — which is the success case here, so it is ignored.
	for _, stmt := range []string{
		`ALTER TABLE nodes ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN session_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN session_type TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("memory: migrate: %s: %w", stmt, err)
		}
	}

	// Clean up nameless concept nodes left by a placement fallback that wrote an
	// empty concept key whenever the inference LLM was unavailable. They carry
	// no content and no embedding — a concept key is only ever a label — so the
	// only thing they contribute is a blank "Concept:" line in every tree
	// render. Facts are attached by topic_name, not by an edge to the concept
	// row, so removing these loses nothing.
	if res, err := db.Exec(`DELETE FROM nodes WHERE type = 'concept' AND TRIM(key) = ''`); err != nil {
		return fmt.Errorf("memory: migrate: drop nameless concepts: %w", err)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("memory: removed nameless concept nodes", "count", n)
	}

	// Indexes covering the project-scoped access paths. They are created after
	// the ALTERs because the columns must exist first on upgraded databases.
	_, err = db.Exec(`
	CREATE INDEX IF NOT EXISTS idx_nodes_project ON nodes(project_id, type, created_at);
	CREATE INDEX IF NOT EXISTS idx_nodes_project_session ON nodes(project_id, session_id, "order");
	CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
	`)
	return err
}
