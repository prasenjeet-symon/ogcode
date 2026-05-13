package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
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
type Graph struct {
	Store *Store
	Chat  ChatClient
	Embed EmbedClient
}

// GraphOptions tunes Graph inference behavior.
type GraphOptions struct {
	SessionID string
	Question  string
	Response  string
	UserTopic string
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

	if err := g.Store.EnsureSession(opts.SessionID); err != nil {
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

	placement, related, err := g.inferPlacement(ctx, opts, topics, existingConcepts, content)
	if err != nil {
		return nil, fmt.Errorf("infer placement: %w", err)
	}

	topicNode := &Node{
		SessionID: opts.SessionID,
		Type:      TypeTopic,
		Key:       placement.Topic,
		Content:   "",
		TopicName: placement.Topic,
	}
	topicNode, err = g.Store.AddNode(*topicNode)
	if err != nil {
		return nil, fmt.Errorf("add topic node: %w", err)
	}

	conceptNode := &Node{
		SessionID: opts.SessionID,
		Type:      TypeConcept,
		Key:       placement.Concept,
		Content:   "",
		TopicName: placement.Topic,
	}
	if placement.Concept == placement.Topic {
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
		SessionID: opts.SessionID,
		Type:      TypeFact,
		Key:       factKey,
		Content:   content,
		Question:  opts.Question,
		Response:  opts.Response,
		TopicName: placement.Topic,
	}
	factNode, err = g.Store.AddNode(*factNode)
	if err != nil {
		return nil, fmt.Errorf("add fact node: %w", err)
	}

	// Parallel: embed + infer labels/summary.
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		vecs, err := g.Embed.Embed(ctx, []string{content})
		if err == nil && len(vecs) > 0 {
			_ = g.Store.SetEmbedding(opts.SessionID, factNode.Key, vecs[0])
		}
		return nil // eat error; embedding failures shouldn't break storage
	})
	eg.Go(func() error {
		if g.Chat != nil {
			labels, summary := g.inferLabelsAndSummary(ctx, opts.Question, opts.Response, placement.Topic)
			if labels != nil || summary != "" {
				_ = g.Store.UpdateNodeEnrichment(opts.SessionID, factNode.Key, summary, labels)
			}
		}
		return nil
	})
	_ = eg.Wait()

	for _, rel := range related {
		edge := Edge{
			SessionID: opts.SessionID,
			FromKey:   placement.Concept,
			ToKey:     rel.ToConcept,
			RelType:   "related",
			Weight:    rel.Weight,
		}
		existingEdges, _ := g.Store.ListEdges(opts.SessionID)
		exists := false
		for _, e := range existingEdges {
			if e.FromKey == edge.FromKey && e.ToKey == edge.ToKey {
				exists = true
				break
			}
		}
		if !exists {
			_ = g.Store.AddEdge(edge)
		}
	}

	return factNode, nil
}

type Placement struct {
	Topic   string
	Concept string
}

type RelatedConcept struct {
	ToConcept string
	Weight    float32
}

func (g *Graph) inferLabelsAndSummary(ctx context.Context, question, response, topic string) ([]string, string) {
	if g.Chat == nil {
		return nil, ""
	}
	prompt := fmt.Sprintf(`Given this Q&A from topic "%s", generate labels and a summary.

Q: %s
A: %s

Respond with:
1. LABELS: <3-6 comma-separated topic labels, lowercase, no spaces (e.g. auth,jwt,security)
2. SUMMARY: <up to 5 sentences that capture the key points>`,
		topic, question, response)

	resp, err := g.Chat.Chat(ctx, "", prompt)
	if err != nil {
		return nil, ""
	}

	var labels []string
	var summaryLines []string
	for _, line := range strings.Split(strings.TrimSpace(resp), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "LABELS:") {
			tags := strings.TrimPrefix(line, "LABELS:")
			for _, l := range strings.Split(tags, ",") {
				l = strings.TrimSpace(l)
				if l != "" {
					labels = append(labels, strings.ToLower(l))
				}
			}
		} else if strings.HasPrefix(line, "SUMMARY:") {
			summaryLines = append(summaryLines, strings.TrimPrefix(line, "SUMMARY:"))
		}
	}
	summary := strings.TrimSpace(strings.Join(summaryLines, " "))
	return labels, summary
}

func (g *Graph) inferPlacement(ctx context.Context, opts GraphOptions, topics []Node, existingConcepts []Node, content string) (Placement, []RelatedConcept, error) {
	if g.Chat == nil {
		topic := opts.UserTopic
		if topic == "" {
			topic = "General"
		}
		concept := makeKey(content)
		if len(concept) > 60 {
			concept = concept[:60]
		}
		return Placement{Topic: topic, Concept: concept}, nil, nil
	}

	var sb strings.Builder
	sb.WriteString("Existing topics:\n")
	for _, t := range topics {
		sb.WriteString("  - " + t.Key + "\n")
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

	resp, err := g.Chat.Chat(ctx, "", prompt)
	if err != nil {
		return Placement{Topic: "General", Concept: opts.UserTopic}, nil, nil
	}

	text := strings.TrimSpace(resp)
	placement := Placement{Topic: "General"}
	related := []RelatedConcept{}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "TOPIC:") {
			t := strings.TrimPrefix(line, "TOPIC:")
			placement.Topic = strings.TrimSpace(t)
			if placement.Topic == "" {
				placement.Topic = "General"
			}
		} else if strings.HasPrefix(line, "CONCEPT:") {
			c := strings.TrimPrefix(line, "CONCEPT:")
			placement.Concept = strings.TrimSpace(c)
			if placement.Concept == "" {
				placement.Concept = placement.Topic
			}
		} else if strings.HasPrefix(line, "RELATED:") {
			r := strings.TrimPrefix(line, "RELATED:")
			r = strings.TrimSpace(r)
			if r != "" && r != "none" {
				for _, part := range strings.Split(r, ",") {
					part = strings.TrimSpace(part)
					if part != "" && part != placement.Concept {
						related = append(related, RelatedConcept{ToConcept: part, Weight: 0.5})
					}
				}
			}
		}
	}

	if len(placement.Concept) > 80 {
		placement.Concept = placement.Concept[:80]
	}
	return placement, related, nil
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
	conceptMap := make(map[string]*ConceptTree)

	for _, n := range nodes {
		if n.Type == TypeTopic {
			tree[n.Key] = &TopicTree{Name: n.Key, Concepts: []ConceptTree{}}
		}
	}

	for _, n := range nodes {
		if n.Type == TypeConcept {
			parent := n.TopicName
			if tree[parent] == nil {
				tree[parent] = &TopicTree{Name: parent, Concepts: []ConceptTree{}}
			}
			tree[parent].Concepts = append(tree[parent].Concepts, ConceptTree{Name: n.Key, Facts: []Node{}, RelatedConcepts: []string{}})
			conceptMap[n.Key] = &tree[parent].Concepts[len(tree[parent].Concepts)-1]
		} else if n.Type == TypeFact {
			parent := n.TopicName
			if tree[parent] == nil {
				tree[parent] = &TopicTree{Name: parent, Concepts: []ConceptTree{}}
			}
			factConceptName := parent + "-facts"
			if conceptMap[factConceptName] == nil {
				tree[parent].Concepts = append(tree[parent].Concepts, ConceptTree{Name: factConceptName, Facts: []Node{}, RelatedConcepts: []string{}})
				conceptMap[factConceptName] = &tree[parent].Concepts[len(tree[parent].Concepts)-1]
			}
			conceptMap[factConceptName].Facts = append(conceptMap[factConceptName].Facts, n)
		}
	}

	if edges != nil {
		edgeMap := make(map[string][]string)
		for _, e := range edges {
			if e.RelType == "related" {
				edgeMap[e.FromKey] = append(edgeMap[e.FromKey], e.ToKey)
			}
		}
		for key, ct := range conceptMap {
			if rels, ok := edgeMap[key]; ok {
				ct.RelatedConcepts = rels
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
	SessionID      string
	Question       string
	RecentMessages []string // last N raw conversation turns for reference resolution
	MaxRounds      int      // max refinement rounds, default 3
	Threshold      float32  // confidence threshold to stop early, default 0.7
	Limit          int      // max facts in lightweight tree, default 50
	MinScore       float32  // minimum cosine similarity to include fact
	Since          int64
	Until          int64
	FromOrder      int
	ToOrder        int
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

	searchQuery := g.rewriteQuery(ctx, opts.SessionID, opts.Question, opts.RecentMessages)

	var queryVec []float32
	vecs, err := g.Embed.Embed(ctx, []string{searchQuery})
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

	if g.Chat == nil {
		return &RecallResult{
			Answer:     LightweightTreeAsTextWithHighlight(fullTree, allFacts, semanticKeys),
			Confidence: 0,
			Rounds:     0,
			FactsUsed:  len(topFacts),
		}, nil
	}

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

		resp, err := g.Chat.Chat(ctx, "", prompt)
		if err != nil {
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

func enrichTreeWithFollowUp(main, extra map[string]TopicTree) map[string]TopicTree {
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
	for topic, tt := range tree {
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

func skeletonTreeText(tree map[string]TopicTree) string {
	var sb strings.Builder
	for topic, tt := range tree {
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
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
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
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float32
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
			magB += b[i] * b[i]
		}
		magA += a[i] * a[i]
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	sqrt := func(f float32) float32 {
		return float32(math.Sqrt(float64(f)))
	}
	return dot / (sqrt(magA) * sqrt(magB))
}

// needsRewrite returns true when the query contains words or phrases that
// suggest it references something from prior context and should be rewritten.
func needsRewrite(query string) bool {
	lower := strings.ToLower(query)

	// Multi-word phrases checked first (more specific).
	phrases := []string{
		"keep going", "do it", "do that", "do the same",
		"the previous", "the last", "go on", "continue from",
		"same thing", "that approach", "that method", "that function",
		"the one", "the other", "like before", "as before",
	}
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return true
		}
	}

	// Single-word references — check as whole words.
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	})
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}
	refWords := []string{
		"it", "its", "this", "that", "these", "those",
		"they", "them", "their", "he", "she", "him", "her",
		"previous", "last", "above", "there", "same",
		"continue", "again", "another", "other",
	}
	for _, ref := range refWords {
		if wordSet[ref] {
			return true
		}
	}
	return false
}

func (g *Graph) rewriteQuery(ctx context.Context, _ string, rawQuery string, recentMessages []string) string {
	if g.Chat == nil {
		return rawQuery
	}
	// Skip the LLM call entirely when the query appears self-contained.
	if !needsRewrite(rawQuery) {
		return rawQuery
	}
	// Pronouns and references almost always point to the immediately preceding
	// exchanges — recent conversation is sufficient. Memory facts belong in the
	// recall step that follows, not here.
	if len(recentMessages) == 0 {
		return rawQuery
	}

	var rawContext strings.Builder
	for _, line := range recentMessages {
		rawContext.WriteString(line + "\n")
	}

	prompt := fmt.Sprintf(`You are resolving references in a user query to make it fully self-contained.

RECENT CONVERSATION:
%s
LATEST QUERY: %s

Resolve ALL of the following reference types found in the query:
- Pronouns: it, its, this, that, these, those, they, them, he, she
- Temporal/ordinal: "the previous", "the last", "the earlier", "the above", "the one before"
- Action continuations: "continue", "keep going", "do the same", "do it again", "go on"
- Implicit entities: "the API", "the function", "the approach", "the method" when clearly referring to something specific from the conversation above

If the query is already self-contained or the reference cannot be resolved from the conversation above, return it unchanged.
Respond ONLY with the rewritten query, nothing else.`,
		rawContext.String(), rawQuery)

	resp, err := g.Chat.Chat(ctx, "", prompt)
	if err != nil || strings.TrimSpace(resp) == "" {
		return rawQuery
	}
	return strings.TrimSpace(resp)
}

// chatClient adapts an ogcode streaming Provider into a blocking ChatClient.
type chatClient struct {
	provider provider.Provider
	model    string
}

func (c *chatClient) Chat(ctx context.Context, system, prompt string) (string, error) {
	model := c.model
	if model == "" {
		model = c.provider.Models()[0].ID
	}
	req := provider.StreamRequest{
		Model: model,
		System: []string{system},
		Messages: []provider.ModelMessage{
			{Role: "user", Content: json.RawMessage(fmt.Sprintf("%q", prompt))},
		},
		Abort: ctx,
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
