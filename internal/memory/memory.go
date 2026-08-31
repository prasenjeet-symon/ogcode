package memory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// Memory provides the agentic memory lifecycle: read, recall, and write.
// It wraps a local SQLite-backed knowledge graph with optional LLM inference.
type Memory struct {
	Store   *Store
	Graph   *Graph
	enabled bool
}

// GraphOpts holds dependencies for initializing agentic memory.
//
// Embedding is always produced by the inbuilt local embedder
// (gte-small) — there is no embedder configuration. The synthesis LLM
// (topic/concept inference and recall) is NOT configured here: it is supplied
// per request by the caller, using the same provider+model the user selected
// for their session. See WriteMemory and RecallWith.
type GraphOpts struct {
	// EmbedProvider is the provider used for text embeddings. Must satisfy
	// provider.Embedder. In practice this is always the inbuilt LocalEmbedder.
	EmbedProvider provider.Provider
}

// New creates a Memory backed by local SQLite graph store. The synthesis LLM
// is not wired here — it is injected per call via WriteMemory/RecallWith so
// that memory uses the session's currently selected model.
func New(store *Store, opts *GraphOpts) *Memory {
	m := &Memory{Store: store}
	if store != nil {
		m.enabled = true
		m.Graph = &Graph{Store: store}
		if opts != nil && opts.EmbedProvider != nil {
			if e, ok := opts.EmbedProvider.(provider.Embedder); ok {
				m.Graph.Embed = NewEmbedClient(e)
			}
		}
	}
	if m.enabled {
		if m.Graph.Embed == nil {
			slog.Warn("agentic memory: no embedder configured — semantic recall unavailable")
		} else {
			slog.Info("agentic memory enabled", "embedProvider", func() string {
				if opts != nil && opts.EmbedProvider != nil {
					return opts.EmbedProvider.ID()
				}
				return "none"
			}())
		}
	}
	return m
}

// Enabled returns whether agentic memory is active.
func (m *Memory) Enabled() bool {
	return m.enabled && m.Graph != nil
}

// ReadMemory fetches the full session knowledge graph as text.
func (m *Memory) ReadMemory(ctx context.Context, sessionID string) string {
	if !m.Enabled() {
		return ""
	}
	_ = m.Store.EnsureSession(sessionID)

	tree, facts, err := m.Graph.BuildLightweightTree(ctx, sessionID, NodeFilter{}, nil, 0)
	if err != nil {
		slog.Warn("BuildLightweightTree failed", "err", err)
		return ""
	}
	if len(facts) == 0 {
		slog.Info("memory graph empty", "session", sessionID)
		return ""
	}
	result := skeletonTreeText(tree)
	if strings.TrimSpace(result) == "" {
		return ""
	}
	slog.Info("memory graph returned context", "session", sessionID, "len", len(result))
	return result
}

// RecallMemory performs semantic recall for a specific question. chat is the
// synthesis LLM client built from the session's selected provider+model; when
// nil, recall returns the raw semantically filtered tree without synthesis.
func (m *Memory) RecallMemory(ctx context.Context, sessionID, question string, chat ChatClient) (string, error) {
	if !m.Enabled() {
		return "", nil
	}
	if m.Graph.Embed == nil {
		slog.Warn("RecallMemory: no embedder configured")
		return m.ReadMemory(ctx, sessionID), nil
	}
	_ = m.Store.EnsureSession(sessionID)

	result, err := m.Graph.Recall(ctx, RecallOptions{
		SessionID: sessionID,
		Question:  question,
		Limit:     50,
		MaxRounds: 3,
		Threshold: 0.7,
		Chat:      chat,
	})
	if err != nil {
		// Reported rather than flattened to "": the caller renders an empty
		// result as "no relevant past context found", which tells the model
		// memory holds nothing about the question when the truth is that the
		// lookup never ran.
		slog.Warn("memory recall failed", "err", err)
		return "", err
	}

	var display string
	if result.Confidence > 0 {
		display = fmt.Sprintf("[confidence: %.0f%% | rounds: %d | facts used: %d]\n\n%s",
			result.Confidence*100, result.Rounds, result.FactsUsed, result.Answer)
	} else {
		display = result.Answer
	}
	slog.Info("memory recall returned context", "session", sessionID, "len", len(display))
	return display, nil
}

// DefaultProjectSessionTypes are the session types project-scoped recall reads.
// Subagent, note, index and search sessions also write memory, but their turns
// are tool traces rather than conversations with the user — including them
// drowns project recall in noise.
var DefaultProjectSessionTypes = []string{"build", "plan"}

// ProjectRecallRequest is a project-scoped recall query.
type ProjectRecallRequest struct {
	ProjectID string
	Question  string
	Since     int64  // unix ms; 0 = the whole history
	TopicName string // empty = every topic
	// SessionID narrows the search to one conversation. The retrieval pipeline is
	// otherwise unchanged, so a session-scoped answer still arrives dated,
	// attributed and recency-ranked — which plain session recall does not provide.
	SessionID string
	Chat      ChatClient
}

// RecallProjectMemory performs semantic recall across every conversation held in
// a project. chat is the synthesis LLM built from the session's selected model;
// when nil, the assembled cross-session context is returned without synthesis.
func (m *Memory) RecallProjectMemory(ctx context.Context, req ProjectRecallRequest) (string, error) {
	if !m.Enabled() {
		return "", nil
	}
	if m.Graph.Embed == nil {
		slog.Warn("RecallProjectMemory: no embedder configured")
		return "", nil
	}
	if req.ProjectID == "" {
		slog.Warn("RecallProjectMemory: no project id resolved")
		return "", nil
	}

	result, err := m.Graph.ProjectRecall(ctx, ProjectRecallOptions{
		ProjectID:    req.ProjectID,
		Question:     req.Question,
		Since:        req.Since,
		TopicName:    req.TopicName,
		OnlySession:  req.SessionID,
		SessionTypes: DefaultProjectSessionTypes,
		Chat:         req.Chat,
	})
	if err != nil {
		// See RecallMemory: an error is not the same as an empty result.
		slog.Warn("project memory recall failed", "err", err)
		return "", err
	}
	if result.TotalFacts == 0 || strings.TrimSpace(result.Answer) == "" {
		return "", nil
	}

	display := result.Answer
	if result.Confidence > 0 {
		display = fmt.Sprintf("[confidence: %.0f%% | rounds: %d | facts used: %d of %d across %d conversations]\n\n%s",
			result.Confidence*100, result.Rounds, result.FactsUsed, result.TotalFacts, result.SessionsUsed, result.Answer)
	}
	slog.Info("project memory recall returned context",
		"project", req.ProjectID, "len", len(display), "sessions", result.SessionsUsed, "facts", result.TotalFacts)
	return display, nil
}

// Scope identifies the session a memory write belongs to, plus the workspace
// attributes stamped onto every node so the turn is later recallable at project
// scope. ProjectID is a project.Resolve'd directory; SessionType mirrors the
// session store's type ("", "plan", "subagent", …).
type Scope struct {
	SessionID   string
	ProjectID   string
	SessionType string
	SessionName string
}

// WriteMemory persists a conversation turn. chat is the synthesis LLM client
// to use for topic/concept inference and enrichment — it should be built from
// the same provider+model the user selected for the current session. When chat
// is nil, the fact is stored without LLM topic inference (placement falls back
// to heuristic). Synthesis runs in a background goroutine; the chat client is
// captured at dispatch time so it reflects the session's model even though the
// call is asynchronous.
func (m *Memory) WriteMemory(ctx context.Context, scope Scope, question, response string, chat ChatClient) {
	if !m.Enabled() {
		return
	}
	go func() {
		// This goroutine is detached from any request, so a panic here is not
		// contained by anything — it takes the whole server down. Memory is a
		// best-effort side channel; a bad LLM response or an empty model list
		// must not be able to end the process.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("memory: panic while persisting turn; memory write dropped",
					"session", scope.SessionID, "panic", r, "stack", string(debug.Stack()))
			}
		}()

		bgCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		_, err := m.Graph.AddFact(bgCtx, GraphOptions{
			SessionID:   scope.SessionID,
			ProjectID:   scope.ProjectID,
			SessionType: scope.SessionType,
			SessionName: scope.SessionName,
			Question:    question,
			Response:    response,
			Chat:        chat,
		})
		if err != nil {
			slog.Warn("memory_add call failed", "err", err)
		} else {
			slog.Info("memory_add succeeded", "session", scope.SessionID, "project", scope.ProjectID)
		}
	}()
}

// DefaultDBPath returns the default path for the memory database.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.TempDir()
	}
	if p := os.Getenv("OGCODE_MEMORY_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join(home, ".ogcode", "memory.db")
}

// DocumentDefaultCollection is the fallback collection name.
const DocumentDefaultCollection = "default"

// Document is an unstructured text fragment tied to a collection.
type Document struct {
	ID         int64  `json:"id"`
	Collection string `json:"collection"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"createdAt"`
}

// SearchResult pairs a Document with a relevance score.
type SearchResult struct {
	Doc   Document `json:"doc"`
	Score float32  `json:"score"`
}

// CollectionStats is returned by Stats.
type CollectionStats struct {
	Collections int `json:"collections"`
	Documents   int `json:"documents"`
	Nodes       int `json:"nodes"`
	Edges       int `json:"edges"`
}

// Stats returns total counts across all collections and graph tables.
func (m *Memory) Stats(ctx context.Context) (col, doc, nodes, edges int, err error) {
	if m.Store == nil {
		return 0, 0, 0, 0, nil
	}
	if err = m.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_collection`).Scan(&col); err != nil {
		return
	}
	if err = m.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_document`).Scan(&doc); err != nil {
		return
	}
	if err = m.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&nodes); err != nil {
		return
	}
	if err = m.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM edges`).Scan(&edges); err != nil {
		return
	}
	return
}

// CreateCollection inserts a new collection.
func (m *Memory) CreateCollection(ctx context.Context, name string) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := m.Store.DB().ExecContext(ctx,
		`INSERT INTO memory_collection (name, created_at) VALUES (?, ?)`,
		name, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteCollection removes a collection including its documents.
func (m *Memory) DeleteCollection(ctx context.Context, name string) error {
	_, err := m.Store.DB().ExecContext(ctx, `DELETE FROM memory_document WHERE collection = ?`, name)
	if err != nil {
		return err
	}
	_, err = m.Store.DB().ExecContext(ctx, `DELETE FROM memory_collection WHERE name = ?`, name)
	return err
}

// UpsertDocument stores or updates a document and computes its embedding using the currently configured embedder.
func (m *Memory) UpsertDocument(ctx context.Context, collection, content string) (int64, error) {
	if collection == "" {
		collection = DocumentDefaultCollection
	}
	var emb []float32
	var embBlob []byte
	if m.Graph != nil && m.Graph.Embed != nil {
		vecs, err := m.Graph.Embed.Embed(ctx, []string{content})
		if err != nil {
			return 0, fmt.Errorf("embedding failed: %w", err)
		}
		if len(vecs) > 0 {
			emb = vecs[0]
			embBlob = floatsToBytes(emb)
		}
	}
	now := time.Now().UnixMilli()
	res, err := m.Store.DB().ExecContext(ctx,
		`INSERT INTO memory_document (collection, content, embedding, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(collection, content) DO UPDATE SET
			 content = excluded.content,
			 embedding = excluded.embedding,
			 created_at = excluded.created_at`,
		collection, content, embBlob, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// floatsToBytes converts a []float32 to a []byte (little-endian).
func floatsToBytes(f []float32) []byte {
	buf := make([]byte, len(f)*4)
	for i, v := range f {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// bytesToFloats converts a []byte to a []float32 (little-endian).
func bytesToFloats(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// SemanticSearch runs a vector search across a collection.
func (m *Memory) SemanticSearch(ctx context.Context, collection, query string, topK int) ([]SearchResult, error) {
	if m.Graph == nil || m.Graph.Embed == nil {
		return nil, fmt.Errorf("semantic search unavailable: no embedder")
	}
	if collection == "" {
		collection = DocumentDefaultCollection
	}
	vecs, err := m.Graph.Embed.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embedding query failed: %w", err)
	}
	qvec := vecs[0]

	// SQLite does not have a native vector index, so we do brute-force cosine similarity.
	rows, err := m.Store.DB().QueryContext(ctx,
		`SELECT id, content, embedding, created_at FROM memory_document WHERE collection = ?`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []SearchResult
	cosineStart := time.Now()
	docsCompared := 0
	for rows.Next() {
		var id int64
		var content string
		var embBlob []byte
		var created int64
		if err := rows.Scan(&id, &content, &embBlob, &created); err != nil {
			continue
		}
		dvec := bytesToFloats(embBlob)
		if dvec == nil || len(dvec) != len(qvec) {
			continue
		}
		docsCompared++
		score := cosine(qvec, dvec)
		if score < 0 {
			score = 0
		}
		candidates = append(candidates, SearchResult{
			Doc:   Document{ID: id, Collection: collection, Content: content, CreatedAt: created},
			Score: score,
		})
	}
	slog.Info("cosine similarity timing (SemanticSearch)",
		"docs_compared", docsCompared,
		"duration", time.Since(cosineStart),
	)
	_ = rows.Err()

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	return candidates, nil
}

// BackfillEmbeddings embeds only the facts that have no embedding, leaving
// existing vectors untouched. It exists because a fact stored without one is
// invisible to every semantic recall — scanProject and BuildLightweightTree
// both skip it — so a graph that accumulated unembedded facts looks full but
// searches as if it were empty.
//
// Unlike RefreshAll this is incremental and safe to run on every start: it is a
// no-op once the backlog is cleared. It stops at the first context
// cancellation so a shutdown does not have to wait for the whole backlog.
//
// progress, when non-nil, is called after each fact with the number finished
// and the size of the backlog, so a caller can tell the user why the machine is
// busy. It runs on this goroutine and should not block.
func (m *Memory) BackfillEmbeddings(ctx context.Context, progress func(done, total int)) (embedded, failed int, err error) {
	if m.Graph == nil || m.Graph.Embed == nil {
		return 0, 0, fmt.Errorf("backfill unavailable: no embedder")
	}

	// Materialized before any UPDATE: the sqlite driver will not accept a write
	// while a read cursor is open on the same connection.
	rows, err := m.Store.DB().QueryContext(ctx,
		`SELECT session_id, key, question, summary, content FROM nodes
		 WHERE type = 'fact' AND content != '' AND (embedding IS NULL OR embedding = '')`)
	if err != nil {
		return 0, 0, err
	}
	type factRow struct{ sessionID, key, question, summary, content string }
	var pending []factRow
	for rows.Next() {
		var r factRow
		if err := rows.Scan(&r.sessionID, &r.key, &r.question, &r.summary, &r.content); err != nil {
			continue
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	if len(pending) == 0 {
		return 0, 0, nil
	}

	slog.Info("memory: backfilling missing fact embeddings", "facts", len(pending))
	for _, r := range pending {
		if err := ctx.Err(); err != nil {
			slog.Info("memory: backfill stopped early", "embedded", embedded, "remaining", len(pending)-embedded-failed)
			return embedded, failed, nil
		}

		started := time.Now()
		vecs, err := m.Graph.Embed.Embed(ctx, []string{factEmbedText(r.question, r.summary, r.content)})
		took := time.Since(started)

		switch {
		case err != nil || len(vecs) == 0 || len(vecs[0]) == 0:
			failed++
			slog.Warn("memory: backfill could not embed fact", "session", r.sessionID, "key", r.key, "err", err)
		default:
			if err := m.Store.SetEmbedding(r.sessionID, r.key, vecs[0]); err != nil {
				failed++
				slog.Warn("memory: backfill could not persist embedding", "session", r.sessionID, "key", r.key, "err", err)
			} else {
				embedded++
			}
		}

		if progress != nil {
			progress(embedded+failed, len(pending))
		}
		if !restAfterEmbed(ctx, took) {
			slog.Info("memory: backfill stopped early", "embedded", embedded, "remaining", len(pending)-embedded-failed)
			return embedded, failed, nil
		}
	}
	slog.Info("memory: backfill complete", "embedded", embedded, "failed", failed)
	return embedded, failed, nil
}

// backfillRestRatio paces the backfill: after each fact it rests this many times
// the embedding's own duration. The backfill is a repair job with no deadline,
// but the local embedder saturates several cores for a couple of hundred
// milliseconds per fact, so running it flat out pins the machine and competes
// with whatever the user is actually doing. Resting in proportion to the work
// keeps the ratio honest on any hardware — a fast machine still finishes fast, a
// slow one backs off further — where a fixed sleep would not.
const backfillRestRatio = 3

// backfillMaxRest caps a single pause so one pathologically slow fact cannot
// stall the rest of the backlog behind it.
const backfillMaxRest = 2 * time.Second

// backfillRest is how long to rest after an embedding that took the given time.
func backfillRest(took time.Duration) time.Duration {
	rest := took * backfillRestRatio
	if rest > backfillMaxRest {
		rest = backfillMaxRest
	}
	return rest
}

// restAfterEmbed pauses in proportion to how long the last embedding took. It
// reports whether the backfill should continue: a cancellation during the rest
// ends it, so shutdown never waits out a pause.
func restAfterEmbed(ctx context.Context, took time.Duration) bool {
	rest := backfillRest(took)
	if rest <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(rest)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// RefreshAll recomputes all embeddings — both collection documents and graph
// facts — so it is the recovery path after switching embedding provider.
// Without re-embedding the graph nodes, session and project recall keep
// scoring against stale old-dimensionality vectors and (with the cosine
// dimension guard) silently match nothing.
func (m *Memory) RefreshAll(ctx context.Context) error {
	if m.Graph == nil || m.Graph.Embed == nil {
		return fmt.Errorf("refresh unavailable: no embedder")
	}

	// 1) Collection documents (memory_document) — embeddings are stored as
	// little-endian float32 byte blobs.
	docRows, err := m.Store.DB().QueryContext(ctx, `SELECT id, content FROM memory_document`)
	if err != nil {
		return err
	}
	type docRow struct {
		id      int64
		content string
	}
	var docs []docRow
	for docRows.Next() {
		var r docRow
		if err := docRows.Scan(&r.id, &r.content); err != nil {
			continue
		}
		docs = append(docs, r)
	}
	if err := docRows.Err(); err != nil {
		docRows.Close()
		return err
	}
	docRows.Close()
	for _, r := range docs {
		vecs, err := m.Graph.Embed.Embed(ctx, []string{r.content})
		if err != nil || len(vecs) == 0 {
			continue
		}
		embBlob := floatsToBytes(vecs[0])
		_, _ = m.Store.DB().ExecContext(ctx,
			`UPDATE memory_document SET embedding = ? WHERE id = ?`,
			embBlob, r.id)
	}

	// 2) Graph fact nodes — embeddings are stored as JSON arrays (see
	// SetEmbedding). We embed each fact's content, which is what AddFact
	// originally embedded (question + " [ANSWER] " + response). Topic and
	// concept nodes carry no content and no embedding, so we filter to
	// type = 'fact'.
	//
	// Rows are materialized before any UPDATE runs: the sqlite driver does
	// not allow a write while a read cursor is still open on the connection.
	nodeRows, err := m.Store.DB().QueryContext(ctx,
		`SELECT session_id, key, question, summary, content FROM nodes WHERE type = 'fact' AND content != ''`)
	if err != nil {
		return err
	}
	type nodeRow struct {
		sessionID string
		key       string
		question  string
		summary   string
		content   string
	}
	var nodes []nodeRow
	for nodeRows.Next() {
		var r nodeRow
		if err := nodeRows.Scan(&r.sessionID, &r.key, &r.question, &r.summary, &r.content); err != nil {
			continue
		}
		nodes = append(nodes, r)
	}
	if err := nodeRows.Err(); err != nil {
		nodeRows.Close()
		return err
	}
	nodeRows.Close()

	var ok, failed int
	for _, r := range nodes {
		// Same text selection AddFact uses, so a refreshed vector is comparable
		// with a freshly written one. Embedding raw content here while AddFact
		// embeds the summary would put the two halves of the corpus in
		// different regions of the vector space.
		vecs, err := m.Graph.Embed.Embed(ctx, []string{factEmbedText(r.question, r.summary, r.content)})
		if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
			failed++
			slog.Warn("memory: refresh could not embed fact", "session", r.sessionID, "key", r.key, "err", err)
			continue
		}
		embJSON, _ := json.Marshal(vecs[0])
		if _, err := m.Store.DB().ExecContext(ctx,
			`UPDATE nodes SET embedding = ? WHERE session_id = ? AND key = ?`,
			embJSON, r.sessionID, r.key); err != nil {
			failed++
			slog.Warn("memory: refresh could not persist embedding", "session", r.sessionID, "key", r.key, "err", err)
			continue
		}
		ok++
	}
	slog.Info("memory: refresh complete", "facts", len(nodes), "embedded", ok, "failed", failed, "documents", len(docs))
	return nil
}
