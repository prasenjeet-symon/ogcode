package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// ChatClient is the minimal interface the Graph needs to call an LLM.
// It wraps a blocking chat call — callers can adapt streaming providers.
type ChatClient interface {
	Chat(ctx context.Context, system, prompt string) (string, error)
}

// EmbedClient returns embedding vectors for input strings.
type EmbedClient interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// Graph orchestrates the knowledge graph lifecycle.
//
// Embed is the inbuilt local embedder (always present when memory is enabled).
// The synthesis LLM is NOT stored here — it is supplied per call via
// GraphOptions.Chat / RecallOptions.Chat so memory uses the session's
// currently selected model rather than a server-wide default.
type Graph struct {
	Store *Store
	Embed EmbedClient
}

// GraphOptions tunes Graph inference behavior.
type GraphOptions struct {
	SessionID string
	// ProjectID, SessionType and SessionName describe the session's workspace.
	// They are denormalized onto every node written so project-scoped recall can
	// filter and attribute facts without reaching into the session database.
	ProjectID   string
	SessionType string
	SessionName string
	Question    string
	Response    string
	UserTopic   string
	// Chat is the synthesis LLM client used for topic/concept inference and
	// enrichment on this call. It should be built from the session's selected
	// provider+model. When nil, placement and enrichment fall back to
	// heuristics (no LLM call).
	Chat ChatClient
}

// AddFact stores a new fact and reorganizes the graph. Embedding is required.
func (g *Graph) AddFact(ctx context.Context, opts GraphOptions) (*Node, error) {
	if opts.SessionID == "" {
		return nil, fmt.Errorf("AddFact: sessionID is required")
	}
	if opts.Question == "" && opts.Response == "" {
		return nil, fmt.Errorf("AddFact: question or response is required")
	}
	if g.Embed == nil {
		return nil, fmt.Errorf("AddFact: embedder is required for agentic memory")
	}

	content := opts.Response
	if opts.Question != "" && opts.Response != "" {
		content = opts.Question + " [ANSWER] " + opts.Response
	} else if opts.Question != "" {
		content = opts.Question
	}

	if err := g.Store.EnsureSessionMeta(SessionUpsert{
		ID:          opts.SessionID,
		ProjectID:   opts.ProjectID,
		SessionType: opts.SessionType,
		Name:        opts.SessionName,
	}); err != nil {
		return nil, fmt.Errorf("ensure session: %w", err)
	}

	topics, err := g.Store.ListNodes(opts.SessionID, TypeTopic)
	if err != nil {
		return nil, fmt.Errorf("list topics: %w", err)
	}
	existingConcepts, err := g.Store.ListNodes(opts.SessionID, TypeConcept)
	if err != nil {
		return nil, fmt.Errorf("list concepts: %w", err)
	}

	// Topic names already used elsewhere in this project, so a fact written in a
	// new session lands under the established topic instead of founding a
	// near-duplicate. Without this, project recall's topic map fragments into
	// dozens of one-session topics that all mean the same thing.
	var projectTopics []TopicCount
	if opts.ProjectID != "" {
		projectTopics, err = g.Store.ListProjectTopics(opts.ProjectID,
			ProjectFilter{SessionTypes: DefaultProjectSessionTypes}, maxPlacementTopics)
		if err != nil {
			slog.Warn("list project topics failed; placing with session topics only", "err", err)
		}
	}

	placement, related, err := g.inferPlacement(ctx, opts, topics, existingConcepts, projectTopics, content, opts.Chat)
	if err != nil {
		return nil, fmt.Errorf("infer placement: %w", err)
	}

	topicNode := &Node{
		SessionID:   opts.SessionID,
		ProjectID:   opts.ProjectID,
		SessionType: opts.SessionType,
		Type:        TypeTopic,
		Key:         placement.Topic,
		Content:     "",
		TopicName:   placement.Topic,
	}
	topicNode, err = g.Store.AddNode(*topicNode)
	if err != nil {
		return nil, fmt.Errorf("add topic node: %w", err)
	}

	conceptNode := &Node{
		SessionID:   opts.SessionID,
		ProjectID:   opts.ProjectID,
		SessionType: opts.SessionType,
		Type:        TypeConcept,
		Key:         placement.Concept,
		Content:     "",
		TopicName:   placement.Topic,
	}
	// A concept that just restates its topic adds a level with no information,
	// and a blank one is a nameless node in every tree render. Neither is worth
	// storing; the fact still hangs off the topic either way.
	if placement.Concept == placement.Topic || strings.TrimSpace(placement.Concept) == "" {
		conceptNode = nil
	}
	if conceptNode != nil {
		conceptNode, err = g.Store.AddNode(*conceptNode)
		if err != nil {
			return nil, fmt.Errorf("add concept node: %w", err)
		}
	}

	h := sha256.Sum256([]byte(content))
	factKey := makeKey(content) + "-" + hex.EncodeToString(h[:4])

	factNode := &Node{
		SessionID:   opts.SessionID,
		ProjectID:   opts.ProjectID,
		SessionType: opts.SessionType,
		Type:        TypeFact,
		Key:         factKey,
		Content:     content,
		Question:    opts.Question,
		Response:    opts.Response,
		TopicName:   placement.Topic,
	}
	factNode, err = g.Store.AddNode(*factNode)
	if err != nil {
		return nil, fmt.Errorf("add fact node: %w", err)
	}

	// Enrichment before embedding, not alongside it: the summary is the best
	// text to embed. A fact's raw content is the whole turn trace — tool calls,
	// inputs and truncated outputs, averaging ~20 KB — and collapsing that into
	// one 384-dim vector mostly averages the signal away, quite apart from
	// overflowing the model's 512-token window. Five sentences of summary
	// describe the same turn in a form the embedder can actually represent.
	var summary string
	if opts.Chat != nil {
		labels, s := g.inferLabelsAndSummary(ctx, opts.Question, opts.Response, placement.Topic, opts.Chat)
		summary = s
		if labels != nil || summary != "" {
			if err := g.Store.UpdateNodeEnrichment(opts.SessionID, factNode.Key, summary, labels); err != nil {
				slog.Warn("memory: store enrichment failed", "session", opts.SessionID, "key", factNode.Key, "err", err)
			}
		}
	}

	// An embedding failure must not lose the fact, so it stays non-fatal — but
	// it is logged. Silently eating it is how 96% of a live graph ended up
	// unembedded, and therefore invisible to every semantic recall, with nothing
	// in the logs to say so.
	embedText := factEmbedText(opts.Question, summary, content)
	if vecs, err := g.Embed.Embed(ctx, []string{embedText}); err != nil {
		slog.Warn("memory: embedding failed; fact stored but not semantically recallable",
			"session", opts.SessionID, "key", factNode.Key, "chars", len(embedText), "err", err)
	} else if len(vecs) == 0 || len(vecs[0]) == 0 {
		slog.Warn("memory: embedder returned no vector; fact stored but not semantically recallable",
			"session", opts.SessionID, "key", factNode.Key)
	} else if err := g.Store.SetEmbedding(opts.SessionID, factNode.Key, vecs[0]); err != nil {
		slog.Warn("memory: persisting embedding failed", "session", opts.SessionID, "key", factNode.Key, "err", err)
	}

	// Edges hang off a named concept; there is nothing to attach them to when
	// the concept was folded into its topic above.
	if conceptNode != nil && len(related) > 0 {
		// Read the session's edges once. This used to re-read every edge in the
		// session for each related concept, so a turn proposing five links did
		// five full scans to write at most five rows.
		existingEdges, err := g.Store.ListEdges(opts.SessionID)
		if err != nil {
			slog.Warn("memory: listing edges failed; skipping related links", "session", opts.SessionID, "err", err)
		} else {
			seen := make(map[[2]string]bool, len(existingEdges))
			for _, e := range existingEdges {
				seen[[2]string{e.FromKey, e.ToKey}] = true
			}
			for _, rel := range related {
				pair := [2]string{placement.Concept, rel.ToConcept}
				if seen[pair] {
					continue
				}
				if err := g.Store.AddEdge(Edge{
					SessionID: opts.SessionID,
					FromKey:   placement.Concept,
					ToKey:     rel.ToConcept,
					RelType:   "related",
					Weight:    rel.Weight,
				}); err != nil {
					slog.Warn("memory: adding edge failed", "session", opts.SessionID, "err", err)
					continue
				}
				// Guard against duplicates inside this batch too.
				seen[pair] = true
			}
		}
	}

	return factNode, nil
}

// traceProse pulls the assistant's own words out of a stored turn trace,
// dropping the tool scaffolding around them.
//
// A fact's Response is the trace built by the agent loop: repeated
// "--- Assistant iteration ---", "Tool: <name> (status)", "  Input: …",
// "  Output: …" and "  Error: …" lines, with the assistant's prose on
// "Text: …". That scaffolding is near-identical in every fact, so embedding
// the raw trace puts every vector in the same place — measured on a real graph,
// every fact scored 0.80-0.82 against any question, which is no ranking at all.
// The prose is the part that actually distinguishes one turn from another.
//
// A "Text:" value can itself span lines (only the first carries the prefix), so
// prose continues until the next scaffolding line.
func traceProse(response string) string {
	var out []string
	inText := false
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Text: "):
			inText = true
			if v := strings.TrimSpace(strings.TrimPrefix(trimmed, "Text: ")); v != "" {
				out = append(out, v)
			}
		case strings.HasPrefix(trimmed, "Tool: "),
			strings.HasPrefix(trimmed, "Input: "),
			strings.HasPrefix(trimmed, "Output: "),
			strings.HasPrefix(trimmed, "Error: "),
			strings.HasPrefix(trimmed, "--- Assistant iteration ---"):
			inText = false
		case inText && trimmed != "":
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " ")
}

// factEmbedText picks the text that represents a fact in vector space.
//
// The question comes first and is always included: it is the most
// discriminative part of a turn and the thing a later recall query most
// resembles. The body is, in order of preference, the LLM-written summary, the
// assistant's prose recovered from the turn trace, and finally the raw content
// — each fallback a step further from "what this turn was about" and closer to
// "what the agent happened to run".
//
// No length cap is applied here. The embedder clips to its own window and
// reports what it did; duplicating that budget in the caller would put the two
// limits out of step the moment either model changes.
func factEmbedText(question, summary, content string) string {
	body := strings.TrimSpace(summary)
	if body == "" {
		body = traceProse(content)
	}
	if body == "" {
		body = content
	}
	if strings.TrimSpace(question) == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return question
	}
	return question + "\n\n" + body
}

type Placement struct {
	Topic   string
	Concept string
}

type RelatedConcept struct {
	ToConcept string
	Weight    float32
}

// fieldLine matches a "KEY: value" line the way an LLM actually emits it. The
// prompts ask for bare "TOPIC:" lines, but models routinely dress them up as
// "1. TOPIC:", "- TOPIC:", "**TOPIC:**" or "### TOPIC:" — and a strict
// HasPrefix check silently drops every one of those, leaving the caller with
// its zero values. That is how blank concept names reached the store.
var fieldLine = regexp.MustCompile(`^\s*(?:[-*+>#]+\s*|\d+[.)]\s*)*\*{0,2}\s*([A-Z_]+)\s*\*{0,2}\s*:\s*(.*)$`)

// parseField returns the key and value of a "KEY: value" line, tolerating list
// markers and bold. ok is false for lines that carry no field.
func parseField(line string) (key, value string, ok bool) {
	m := fieldLine.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(strings.Trim(strings.TrimSpace(m[2]), "*")), true
}

// conceptFromContent derives a readable concept name from a fact's own text,
// used whenever the placement LLM is unavailable or unparseable. It must never
// return "": a blank key is still written to the store as a node, producing a
// nameless concept that shows up in every tree render.
func conceptFromContent(content string) string {
	concept := strings.TrimSpace(strings.Join(strings.Fields(content), " "))
	if concept == "" {
		return "Unlabelled"
	}
	return truncate(concept, 60)
}

func (g *Graph) inferLabelsAndSummary(ctx context.Context, question, response, topic string, chat ChatClient) ([]string, string) {
	if chat == nil {
		return nil, ""
	}
	prompt := fmt.Sprintf(`Given this Q&A from topic "%s", generate labels and a summary.

Q: %s
A: %s

Respond with:
1. LABELS: <3-6 comma-separated topic labels, lowercase, no spaces (e.g. auth,jwt,security)
2. SUMMARY: <up to 5 sentences that capture the key points>`,
		topic, question, response)

	resp, err := chat.Chat(ctx, "", prompt)
	if err != nil {
		return nil, ""
	}

	var labels []string
	var summaryLines []string
	// The summary is asked for as "up to 5 sentences" but arrives on one line as
	// often as several, so once SUMMARY: is seen every following line belongs to
	// it until another field starts. Collecting only the marker line dropped
	// multi-line summaries entirely.
	inSummary := false
	for _, line := range strings.Split(strings.TrimSpace(resp), "\n") {
		key, value, ok := parseField(line)
		switch {
		case ok && key == "LABELS":
			inSummary = false
			for _, l := range strings.Split(value, ",") {
				if l = strings.TrimSpace(l); l != "" {
					labels = append(labels, strings.ToLower(l))
				}
			}
		case ok && key == "SUMMARY":
			inSummary = true
			if value != "" {
				summaryLines = append(summaryLines, value)
			}
		case ok:
			inSummary = false
		case inSummary:
			if t := strings.TrimSpace(line); t != "" {
				summaryLines = append(summaryLines, t)
			}
		}
	}
	summary := strings.TrimSpace(strings.Join(summaryLines, " "))
	return labels, summary
}

// maxPlacementTopics bounds how many project topics are offered to the
// placement LLM. The list rides on every memory write, so it is capped at the
// largest topics rather than the whole taxonomy.
const maxPlacementTopics = 50

func (g *Graph) inferPlacement(ctx context.Context, opts GraphOptions, topics []Node, existingConcepts []Node, projectTopics []TopicCount, content string, chat ChatClient) (Placement, []RelatedConcept, error) {
	if chat == nil {
		topic := opts.UserTopic
		if topic == "" {
			topic = "General"
		}
		return Placement{Topic: topic, Concept: conceptFromContent(content)}, nil, nil
	}

	var sb strings.Builder
	sb.WriteString("Existing topics in this conversation:\n")
	for _, t := range topics {
		sb.WriteString("  - " + t.Key + "\n")
	}
	if len(projectTopics) > 0 {
		sb.WriteString("Topics already used elsewhere in this project (fact counts):\n")
		for _, t := range projectTopics {
			sb.WriteString(fmt.Sprintf("  - %s (%d)\n", t.Name, t.Facts))
		}
	}
	sb.WriteString("Existing concepts:\n")
	for _, c := range existingConcepts {
		sb.WriteString("  - [" + c.TopicName + "] " + c.Key + "\n")
	}

	userTopicHint := ""
	if opts.UserTopic != "" {
		userTopicHint = "\nUser requested topic: " + opts.UserTopic + "\n"
	}

	prompt := fmt.Sprintf(`You are organizing conversation memory into a knowledge graph.
Structure: Topic → Concept (grouped fact) → Fact (leaf knowledge).

%s
New fact to organize: %q
%s

Reuse an existing topic name VERBATIM whenever the fact plausibly belongs to it —
topics listed as used elsewhere in the project count, and matching one of those is
how knowledge from different conversations ends up connected. Only invent a new
topic when the fact genuinely fits none of them; never coin a near-synonym of a
listed topic (e.g. do not add "Auth" when "Auth System" exists).

Respond with exactly 3 lines:
1. TOPIC: <topic name or "General" if no topic fits>
2. CONCEPT: <concept name (can be same as topic if no grouping is needed)>
3. RELATED: <comma-separated list of already-existing concept names this relates to (e.g. "REST conventions, JWT tokens") or "none">

Examples:
  Fact: "JWT tokens expire in 1 hour"
  TOPIC: Auth System
  CONCEPT: JWT tokens expire in 1h
  RELATED: Auth Middleware, User roles

  Fact: "v2 endpoints use /api/v2 prefix"
  TOPIC: API Design
  CONCEPT: REST conventions
  RELATED: none`, sb.String(), content, userTopicHint)

	resp, err := chat.Chat(ctx, "", prompt)
	if err != nil {
		// No caller sets UserTopic, so this used to hand back a blank concept
		// name — which AddFact then stored as a nameless concept node.
		slog.Warn("memory: placement inference failed; filing under General",
			"session", opts.SessionID, "err", err)
		topic := opts.UserTopic
		if topic == "" {
			topic = "General"
		}
		return Placement{Topic: topic, Concept: conceptFromContent(content)}, nil, nil
	}

	text := strings.TrimSpace(resp)
	placement := Placement{Topic: "General"}
	related := []RelatedConcept{}

	for _, line := range strings.Split(text, "\n") {
		key, value, ok := parseField(line)
		if !ok {
			continue
		}
		switch key {
		case "TOPIC":
			placement.Topic = value
			if placement.Topic == "" {
				placement.Topic = "General"
			}
		case "CONCEPT":
			placement.Concept = value
		case "RELATED":
			if value == "" || strings.EqualFold(value, "none") {
				continue
			}
			for _, part := range strings.Split(value, ",") {
				if part = strings.TrimSpace(part); part != "" {
					related = append(related, RelatedConcept{ToConcept: part, Weight: 0.5})
				}
			}
		}
	}

	// Resolved after the loop, not inside it: CONCEPT may be parsed before
	// TOPIC, and a self-referential RELATED entry can only be filtered once the
	// concept name is final.
	if placement.Concept == "" {
		placement.Concept = conceptFromContent(content)
	}
	placement.Concept = truncate(placement.Concept, 80)
	kept := related[:0]
	for _, r := range related {
		if r.ToConcept != placement.Concept {
			kept = append(kept, r)
		}
	}
	return placement, kept, nil
}

// ──── Tree building ────

// TopicTree is the readable tree for one topic.
type TopicTree struct {
	Name     string        `json:"name"`
	Concepts []ConceptTree `json:"concepts"`
}

// ConceptTree is the readable tree for one concept.
type ConceptTree struct {
	Name            string   `json:"name"`
	Facts           []Node   `json:"facts"`
	RelatedConcepts []string `json:"related,omitempty"`
}

// BuildTree constructs the hierarchical memory tree for a session.
func (g *Graph) BuildTree(ctx context.Context, sessionID string) (map[string]TopicTree, error) {
	return g.BuildTreeFiltered(ctx, sessionID, NodeFilter{})
}

// BuildTreeFiltered constructs the tree filtered by given bounds.
func (g *Graph) BuildTreeFiltered(ctx context.Context, sessionID string, f NodeFilter) (map[string]TopicTree, error) {
	nodes, err := g.Store.ListNodesFiltered(sessionID, f)
	if err != nil {
		return nil, err
	}
	edges, err := g.Store.ListEdges(sessionID)
	if err != nil {
		return nil, err
	}
	return buildTreeFromNodes(nodes, edges), nil
}

// BuildLightweightTree builds a lightweight tree ready for LLM consumption.
func (g *Graph) BuildLightweightTree(ctx context.Context, sessionID string, f NodeFilter, queryVec []float32, limit int) (map[string]TopicTree, []Node, error) {
	f.Type = TypeFact
	allNodes, err := g.Store.ListNodesFiltered(sessionID, f)
	if err != nil {
		return nil, nil, err
	}
	if len(allNodes) == 0 {
		return nil, nil, nil
	}

	var topFacts []Node

	if queryVec != nil && g.Embed != nil && limit > 0 {
		embeddings, _ := g.Store.Embeddings(sessionID)
		maxOrder := 1
		for _, n := range allNodes {
			if n.Order > maxOrder {
				maxOrder = n.Order
			}
		}

		type scored struct {
			node  Node
			score float32
		}
		var scoredFacts []scored

		cosinStart := time.Now()
		for _, n := range allNodes {
			if emb, ok := embeddings[n.Key]; ok && len(emb) > 0 {
				baseScore := cosine(queryVec, emb)
				if baseScore > 0.1 {
					recencyBoost := (float32(n.Order) / float32(maxOrder)) * 0.15
					scoredFacts = append(scoredFacts, scored{node: n, score: baseScore + recencyBoost})
				}
			}
		}
		slog.Info("cosine similarity timing (BuildLightweightTree)",
			"facts_compared", len(allNodes),
			"duration", time.Since(cosinStart),
		)

		sort.Slice(scoredFacts, func(i, j int) bool { return scoredFacts[i].score > scoredFacts[j].score })
		if len(scoredFacts) > limit {
			scoredFacts = scoredFacts[:limit]
		}

		// N-1 / N+1 Context Windowing
		targetOrders := make(map[int]bool)
		for _, s := range scoredFacts {
			targetOrders[s.node.Order] = true
			targetOrders[s.node.Order-1] = true
			targetOrders[s.node.Order+1] = true
		}

		for _, n := range allNodes {
			if targetOrders[n.Order] {
				topFacts = append(topFacts, n)
			}
		}
		sort.Slice(topFacts, func(i, j int) bool { return topFacts[i].Order < topFacts[j].Order })
	} else {
		sort.Slice(allNodes, func(i, j int) bool {
			return allNodes[i].Order > allNodes[j].Order
		})
		if limit > 0 && len(allNodes) > limit {
			allNodes = allNodes[:limit]
		}
		topFacts = allNodes
	}

	if len(topFacts) == 0 {
		return nil, nil, nil
	}

	tree := buildTreeFromNodes(topFacts, nil)
	return tree, topFacts, nil
}

func buildTreeFromNodes(nodes []Node, edges []Edge) map[string]TopicTree {
	tree := make(map[string]*TopicTree)
	// Concepts are addressed by (topic, index), never by pointer. Holding a
	// *ConceptTree into a slice is only safe until the next append to that same
	// slice reallocates it, after which every stored pointer writes into the
	// abandoned array and the update is silently lost — facts vanishing from a
	// topic that had more than one concept.
	type conceptRef struct {
		topic string
		i     int
	}
	conceptIdx := make(map[string]conceptRef)

	topicOf := func(parent string) *TopicTree {
		if tree[parent] == nil {
			tree[parent] = &TopicTree{Name: parent, Concepts: []ConceptTree{}}
		}
		return tree[parent]
	}
	addConcept := func(parent, name string) conceptRef {
		tt := topicOf(parent)
		tt.Concepts = append(tt.Concepts, ConceptTree{Name: name, Facts: []Node{}, RelatedConcepts: []string{}})
		return conceptRef{topic: parent, i: len(tt.Concepts) - 1}
	}

	for _, n := range nodes {
		if n.Type == TypeTopic {
			tree[n.Key] = &TopicTree{Name: n.Key, Concepts: []ConceptTree{}}
		}
	}

	for _, n := range nodes {
		switch n.Type {
		case TypeConcept:
			conceptIdx[n.Key] = addConcept(n.TopicName, n.Key)
		case TypeFact:
			factConceptName := n.TopicName + "-facts"
			ref, ok := conceptIdx[factConceptName]
			if !ok {
				ref = addConcept(n.TopicName, factConceptName)
				conceptIdx[factConceptName] = ref
			}
			ct := &tree[ref.topic].Concepts[ref.i]
			ct.Facts = append(ct.Facts, n)
		}
	}

	if edges != nil {
		edgeMap := make(map[string][]string)
		for _, e := range edges {
			if e.RelType == "related" {
				edgeMap[e.FromKey] = append(edgeMap[e.FromKey], e.ToKey)
			}
		}
		for key, ref := range conceptIdx {
			if rels, ok := edgeMap[key]; ok {
				tree[ref.topic].Concepts[ref.i].RelatedConcepts = rels
			}
		}
	}

	out := make(map[string]TopicTree)
	for k, v := range tree {
		out[k] = *v
	}
	return out
}

// ──── Recall ────

type RecallOptions struct {
	SessionID string
	Question  string
	MaxRounds int     // max refinement rounds, default 3
	Threshold float32 // confidence threshold to stop early, default 0.7
	Limit     int     // max facts in lightweight tree, default 50
	MinScore  float32 // minimum cosine similarity to include fact
	Since     int64
	Until     int64
	FromOrder int
	ToOrder   int
	// Chat is the synthesis LLM client used for the convergence refinement
	// loop. It should be built from the session's selected provider+model.
	// When nil, recall returns the raw semantically filtered tree without
	// LLM synthesis.
	Chat ChatClient
}

type RecallResult struct {
	Answer     string
	Confidence float32
	Rounds     int
	FactsUsed  int
}

func (g *Graph) Recall(ctx context.Context, opts RecallOptions) (*RecallResult, error) {
	if opts.MaxRounds == 0 {
		opts.MaxRounds = 3
	}
	if opts.Threshold == 0 {
		opts.Threshold = 0.7
	}
	if opts.Limit == 0 {
		opts.Limit = 50
	}
	if g.Embed == nil {
		return nil, fmt.Errorf("Recall: embedder is required for agentic memory")
	}

	filter := NodeFilter{
		Since:     opts.Since,
		Until:     opts.Until,
		FromOrder: opts.FromOrder,
		ToOrder:   opts.ToOrder,
	}

	var queryVec []float32
	vecs, err := g.Embed.Embed(ctx, []string{opts.Question})
	if err == nil && len(vecs) > 0 {
		queryVec = vecs[0]
	}

	fullTree, allFacts, err := g.BuildLightweightTree(ctx, opts.SessionID, filter, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("build full tree: %w", err)
	}

	semanticTree, topFacts, err := g.BuildLightweightTree(ctx, opts.SessionID, filter, queryVec, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("build semantic tree: %w", err)
	}

	semanticKeys := make(map[string]float32)
	for _, f := range topFacts {
		semanticKeys[f.Key] = 0
	}

	if opts.Chat == nil {
		return &RecallResult{
			Answer:     LightweightTreeAsTextWithHighlight(fullTree, allFacts, semanticKeys),
			Confidence: 0,
			Rounds:     0,
			FactsUsed:  len(topFacts),
		}, nil
	}
	chat := opts.Chat

	skeletonTree := make(map[string]TopicTree)
	for k, tt := range fullTree {
		skeleton := TopicTree{Name: tt.Name, Concepts: make([]ConceptTree, len(tt.Concepts))}
		for i, ct := range tt.Concepts {
			skeleton.Concepts[i] = ConceptTree{Name: ct.Name, RelatedConcepts: ct.RelatedConcepts, Facts: ct.Facts}
		}
		skeletonTree[k] = skeleton
	}

	var (
		round       int
		convergence bool
		confidence  float32 = 1.0
		bestAnswer  string
		history     []string
	)

	for round < opts.MaxRounds && !convergence {
		round++
		prompt := buildRecallPrompt(opts.Question, skeletonTree, semanticTree, topFacts, history)

		resp, err := chat.Chat(ctx, "", prompt)
		if err != nil {
			// Rounds after the first only refine an answer that already exists.
			// Failing the whole recall discards it, and the caller reports "no
			// relevant past context found" — a false negative the model cannot
			// tell apart from a genuine miss. Keep what converged so far.
			if bestAnswer != "" {
				slog.Warn("memory: recall refinement failed; returning the previous round's answer",
					"round", round, "err", err)
				round--
				break
			}
			return nil, fmt.Errorf("recall round %d: %w", round, err)
		}

		parsed := parseRecallResponse(resp)

		if !parsed.ContextFound || parsed.FinalContext == "EMPTY_CONTEXT" {
			bestAnswer = ""
			confidence = 1.0
			convergence = true
			break
		}

		bestAnswer = parsed.FinalContext
		confidence = parsed.Confidence

		if (!parsed.RefinementNeeded && parsed.Confidence >= opts.Threshold) || round >= opts.MaxRounds {
			convergence = true
			break
		}

		if parsed.Critique != "" || parsed.DraftContext != "" || parsed.FinalContext != "" {
			if parsed.DraftContext != "" {
				history = append(history, fmt.Sprintf("Round %d DRAFT_CONTEXT: %s", round, parsed.DraftContext))
			}
			if parsed.FinalContext != "" && parsed.FinalContext != "EMPTY_CONTEXT" {
				history = append(history, fmt.Sprintf("Round %d FINAL_CONTEXT: %s", round, parsed.FinalContext))
			}
			if parsed.Critique != "" {
				history = append(history, fmt.Sprintf("Round %d CRITIQUE: %s", round, parsed.Critique))
			}
			history = append(history, "Instruction for next round: Rewrite FINAL_CONTEXT by fixing all issues identified in the CRITIQUE. Preserve facts that were correct.")
		}

		if parsed.FollowUp != "" {
			followupVec, err := g.Embed.Embed(ctx, []string{parsed.FollowUp})
			var followupTree map[string]TopicTree
			var followupFacts []Node
			if err == nil && len(followupVec) > 0 {
				followupTree, followupFacts, err = g.BuildLightweightTree(ctx, opts.SessionID, filter, followupVec[0], opts.Limit)
			}
			if err != nil || len(followupFacts) == 0 {
				// Fallback: fetch recent facts without semantic filter
				followupTree, followupFacts, err = g.BuildLightweightTree(ctx, opts.SessionID, filter, nil, 20)
			}
			if err == nil && len(followupFacts) > 0 {
				// Merge followup facts into the main semantic set so they appear in Round N+1
				mergedTree := enrichTreeWithFollowUp(semanticTree, followupTree)
				semanticTree = mergedTree
				// Deduplicate merged facts by key
				seen := make(map[string]bool)
				for _, f := range topFacts {
					seen[f.Key] = true
				}
				for _, f := range followupFacts {
					if !seen[f.Key] {
						topFacts = append(topFacts, f)
						seen[f.Key] = true
					}
				}
				// Also enrich the bird's-eye skeleton so new topics are navigable
				fullTree = enrichTreeWithFollowUp(fullTree, followupTree)
				skeletonTree = make(map[string]TopicTree)
				for k, tt := range fullTree {
					skeleton := TopicTree{Name: tt.Name, Concepts: make([]ConceptTree, len(tt.Concepts))}
					for i, ct := range tt.Concepts {
						skeleton.Concepts[i] = ConceptTree{Name: ct.Name, RelatedConcepts: ct.RelatedConcepts, Facts: ct.Facts}
					}
					skeletonTree[k] = skeleton
				}
			}
			history = append(history, fmt.Sprintf("Round %d — Follow-up searched: %s", round, parsed.FollowUp))
		}
	}

	return &RecallResult{
		Answer:     bestAnswer,
		Confidence: confidence,
		Rounds:     round,
		FactsUsed:  len(topFacts),
	}, nil
}

type recallResponse struct {
	DraftContext     string
	FinalContext     string
	ContextFound     bool
	Confidence       float32
	FactsUsed        int
	FollowUp         string
	Critique         string
	RefinementNeeded bool
}

func parseRecallResponse(text string) recallResponse {
	// Strip optional markdown code fence the LLM may wrap around JSON.
	raw := strings.TrimSpace(text)
	if strings.HasPrefix(raw, "```") {
		if i := strings.Index(raw, "\n"); i != -1 {
			raw = raw[i+1:]
		}
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
	}

	var j struct {
		ThoughtProcess   string  `json:"thought_process"`
		ContextFound     bool    `json:"context_found"`
		DraftContext     string  `json:"draft_context"`
		Critique         string  `json:"critique"`
		RefinementNeeded bool    `json:"refinement_needed"`
		Confidence       float32 `json:"confidence"`
		FollowUp         string  `json:"followup"`
		FinalContext     string  `json:"final_context"`
		FactsUsed        int     `json:"facts_used"`
	}
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		// Fallback: treat the entire response as the final context.
		return recallResponse{FinalContext: strings.TrimSpace(text), Confidence: 1.0}
	}
	return recallResponse{
		DraftContext:     j.DraftContext,
		FinalContext:     j.FinalContext,
		ContextFound:     j.ContextFound,
		Confidence:       j.Confidence,
		FactsUsed:        j.FactsUsed,
		FollowUp:         j.FollowUp,
		Critique:         j.Critique,
		RefinementNeeded: j.RefinementNeeded,
	}
}

func buildRecallPrompt(question string, skeletonTree map[string]TopicTree, semanticTree map[string]TopicTree, topFacts []Node, history []string) string {
	var sb strings.Builder
	sb.WriteString("You are a Context Enricher. Your sole job: given an incoming query, produce a tight\n")
	sb.WriteString("background context block that frames the query for a downstream LLM.\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- NEVER answer the query. Surface context around it, not a response to it.\n")
	sb.WriteString("- Be specific and to the point. No preamble, no prose filler, no repetition.\n")
	sb.WriteString("- Use bullet points. Cover all relevant facts — do not artificially limit — but omit everything irrelevant.\n")
	sb.WriteString("- Organize bullets in chronological order (oldest → newest) so the downstream LLM sees how things evolved.\n")
	sb.WriteString("- Group related bullets under a short timeline label (e.g. [Earlier] / [Recently] / [Latest]) when it aids clarity.\n\n")

	if len(history) > 0 {
		sb.WriteString("Previous retrieval rounds:\n")
		for _, h := range history {
			sb.WriteString("  " + h + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("=== TOPIC MAP ===\n")
	sb.WriteString(skeletonTreeText(skeletonTree))

	sb.WriteString("\n=== RELEVANT FACTS (★ = semantic match) ===\n")
	sb.WriteString(semanticTreeText(semanticTree, topFacts))
	sb.WriteString("\nQuery: " + question + "\n\n")
	sb.WriteString(`Respond with a single JSON object, no markdown fences:
{
  "context_found": <true|false — false if memory has nothing relevant to this query>,
  "draft_context": "<Working draft of the context block — bullet points, chronological. Used for self-correction in next round. Empty if context_found is false.>",
  "critique": "<Did you accidentally answer the query? Include irrelevant facts? Miss important timeline ordering? Empty if no issues.>",
  "refinement_needed": <true|false — true if critique found issues or a followup search would help>,
  "confidence": <0.0-1.0>,
  "followup": "<specific topic/fact to search next if refinement_needed, else empty>",
  "facts_used": <integer>,
  "final_context": "<Chronologically ordered bullet-point context block (• prefix, timeline labels where helpful) covering all relevant background for the downstream LLM. Specific, no fluff. Empty string if context_found is false.>"
}`)
	return sb.String()
}

// enrichTreeWithFollowUp merges a follow-up round's topics into the running
// tree and returns the result.
//
// main is routinely nil. BuildLightweightTree returns a nil map for "nothing
// matched" — not only for an empty session, but also when the session has facts
// and none of them clear the cosine threshold, which is the common case for a
// question about something not discussed yet. Recall keeps that nil in
// semanticTree, and the follow-up round then merges into it: the follow-up's
// own fallback query drops the semantic filter entirely, so it reliably returns
// recent facts and reliably reached this write. A nil map reads fine and panics
// only when written, so the failure surfaced here rather than at the source.
func enrichTreeWithFollowUp(main, extra map[string]TopicTree) map[string]TopicTree {
	if main == nil {
		main = make(map[string]TopicTree, len(extra))
	}
	for topic, tt := range extra {
		if existing, ok := main[topic]; ok {
			main[topic] = TopicTree{
				Name:     existing.Name,
				Concepts: append(existing.Concepts, tt.Concepts...),
			}
		} else {
			main[topic] = tt
		}
	}
	return main
}

// ──── Text output ────

func LightweightTreeAsText(tree map[string]TopicTree, topFacts []Node) string {
	return lightweightTreeAsText(tree, topFacts, nil, 0)
}

func LightweightTreeAsTextWithHighlight(tree map[string]TopicTree, topFacts []Node, relevantKeys map[string]float32) string {
	return lightweightTreeAsText(tree, topFacts, relevantKeys, 0)
}

func LightweightTreeAsTextWithLimit(tree map[string]TopicTree, topFacts []Node, maxSummaryChars int) string {
	return lightweightTreeAsText(tree, topFacts, nil, maxSummaryChars)
}

func lightweightTreeAsText(tree map[string]TopicTree, topFacts []Node, relevantKeys map[string]float32, summaryLimit int) string {
	topicLabelMap := make(map[string]map[string]bool)
	for _, f := range topFacts {
		if topicLabelMap[f.TopicName] == nil {
			topicLabelMap[f.TopicName] = make(map[string]bool)
		}
		for _, l := range f.Labels {
			topicLabelMap[f.TopicName][l] = true
		}
	}

	var sb strings.Builder
	for _, topic := range sortedTopics(tree) {
		tt := tree[topic]
		sb.WriteString("Topic: " + topic)
		if labels := dedupSortMap(topicLabelMap[topic]); len(labels) > 0 {
			sb.WriteString(" [labels: " + strings.Join(labels, ", ") + "]")
		}
		sb.WriteString("\n")

		for _, ct := range tt.Concepts {
			sb.WriteString("  Concept: " + ct.Name + "\n")
			for _, f := range ct.Facts {
				prefix := "    └── [order:" + fmt.Sprintf("%d", f.Order) + "]"
				if f.CreatedAt > 0 {
					prefix += " | " + time.UnixMilli(f.CreatedAt).Format("2006-01-02 15:04")
				}
				if relevantKeys != nil {
					if _, ok := relevantKeys[f.Key]; ok {
						prefix = strings.Replace(prefix, "└──", "★", 1)
					}
				}
				if len(f.Labels) > 0 {
					prefix += " [labels:" + strings.Join(f.Labels, ",") + "]"
				}
				prefix += " "

				text := factDisplayText(f, summaryLimit)
				if f.Question != "" {
					sb.WriteString(prefix + "Q: " + f.Question + "\n    A: " + text + "\n")
				} else {
					sb.WriteString(prefix + text + "\n")
				}
			}
			if len(ct.RelatedConcepts) > 0 {
				sb.WriteString("    Related: " + strings.Join(ct.RelatedConcepts, ", ") + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func dedupSortMap(m map[string]bool) []string {
	if m == nil {
		return nil
	}
	var out []string
	for l := range m {
		out = append(out, l)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func factDisplayText(f Node, limit int) string {
	text := f.Response
	if text == "" {
		text = f.Content
	}
	if limit > 0 && len(text) > limit {
		return truncate(text, limit)
	}
	return text
}

func fullTreeText(tree map[string]TopicTree, allFacts []Node) string {
	return LightweightTreeAsText(tree, allFacts)
}

func semanticTreeText(semanticTree map[string]TopicTree, topFacts []Node) string {
	relevantKeys := make(map[string]float32)
	for _, f := range topFacts {
		relevantKeys[f.Key] = 0
	}
	return LightweightTreeAsTextWithHighlight(semanticTree, topFacts, relevantKeys)
}

// sortedTopics gives the tree renderers a stable order. Ranging a Go map
// reshuffles topics on every call, so two identical recalls produced different
// prompts, and the "chronological order" instruction had to fight a randomly
// permuted topic list to be followed.
func sortedTopics(tree map[string]TopicTree) []string {
	out := make([]string, 0, len(tree))
	for k := range tree {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func skeletonTreeText(tree map[string]TopicTree) string {
	var sb strings.Builder
	for _, topic := range sortedTopics(tree) {
		tt := tree[topic]
		sb.WriteString("Topic: " + topic + "\n")
		for _, ct := range tt.Concepts {
			sb.WriteString("  Concept: " + ct.Name + "\n")
			for _, f := range ct.Facts {
				line := "    └── [order:" + fmt.Sprintf("%d", f.Order) + "]"
				if f.CreatedAt > 0 {
					line += " | " + time.UnixMilli(f.CreatedAt).Format("2006-01-02 15:04")
				}
				if len(f.Labels) > 0 {
					line += " [labels:" + strings.Join(f.Labels, ",") + "]"
				}
				line += " "
				text := f.Summary
				if text == "" {
					text = truncate(f.Content, 100)
				}
				sb.WriteString(line + text + "\n")
			}
			if len(ct.RelatedConcepts) > 0 {
				sb.WriteString("    Related: " + strings.Join(ct.RelatedConcepts, ", ") + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	// Byte-slicing a string can split a multi-byte UTF-8 sequence, producing
	// invalid UTF-8 that renders as replacement characters. Walk by rune and
	// cut at a character boundary so non-ASCII text survives truncation.
	if len(s) <= max {
		return s
	}
	// Fast path: if the cut point is already on a rune boundary, slice directly.
	if utf8.RuneStart(s[max]) {
		return s[:max] + "..."
	}
	// Otherwise back up to the last rune boundary at or before max bytes.
	for i := max; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i] + "..."
		}
	}
	return "..."
}

func makeKey(content string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if r == '\t' {
			return ' '
		}
		return r
	}, content)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > 60 {
		cleaned = cleaned[:60]
	}
	cleaned = strings.ToLower(cleaned)
	cleaned = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, cleaned)
	if cleaned == "" {
		return "fact"
	}
	return cleaned
}

func cosine(a, b []float32) float32 {
	// Vectors from different embedding models have different dimensionalities.
	// Comparing them is meaningless — a partial overlap produces a score that
	// looks valid but is mathematically wrong. Skip mismatched vectors entirely
	// so a provider switch degrades to "no match" instead of "wrong match".
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float32
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	sqrt := func(f float32) float32 {
		return float32(math.Sqrt(float64(f)))
	}
	return dot / (sqrt(magA) * sqrt(magB))
}

// chatClient adapts an ogcode streaming Provider into a blocking ChatClient.
type chatClient struct {
	provider provider.Provider
	model    string
}

func (c *chatClient) Chat(ctx context.Context, system, prompt string) (string, error) {
	model := c.model
	if model == "" {
		models := c.provider.Models()
		if len(models) == 0 {
			return "", fmt.Errorf("memory: provider %q has no models to synthesize with", c.provider.ID())
		}
		model = models[0].ID
	}
	req := provider.StreamRequest{
		Model:  model,
		System: []string{system},
		Messages: []provider.ModelMessage{
			{Role: "user", Content: json.RawMessage(fmt.Sprintf("%q", prompt))},
		},
	}
	ch, err := c.provider.StreamChat(ctx, req)
	if err != nil {
		return "", err
	}
	var parts []string
	for ev := range ch {
		if ev.Type == provider.EventTextDelta {
			parts = append(parts, ev.Text)
		} else if ev.Type == provider.EventError {
			return "", fmt.Errorf("chat error: %s", ev.Error)
		}
	}
	return strings.Join(parts, ""), nil
}

// NewChatClient creates a blocking ChatClient from an ogcode Provider.
// model is the specific model ID to use; if empty the provider's default (Models()[0]) is used.
func NewChatClient(p provider.Provider, model string) ChatClient {
	return &chatClient{provider: p, model: model}
}

// embedClient wraps an ogcode provider.Embedder as an EmbedClient.
type embedClient struct {
	e provider.Embedder
}

func (c *embedClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	return c.e.Embed(ctx, inputs)
}

// NewEmbedClient creates an EmbedClient from an ogcode Embedder.
func NewEmbedClient(e provider.Embedder) EmbedClient {
	return &embedClient{e: e}
}

func init() {}
