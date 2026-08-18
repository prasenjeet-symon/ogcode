package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
)

// Project-scoped recall answers questions against every conversation ever held
// in a workspace, where session-scoped recall (see Graph.Recall) only sees the
// current one.
//
// It is a separate path rather than a wider WHERE clause because three things
// in the session algorithm are session-local by construction:
//
//   - "order" numbers restart at 1 in every session, so the neighbour windowing
//     and the recency boost derived from them are meaningless across sessions.
//   - The session tree is small enough to hand to the LLM whole. A project graph
//     can be hundreds of times larger, so the bird's-eye view has to be an
//     aggregate (topic counts and date ranges) instead of every fact.
//   - A fact carries no meaning without its conversation. Project answers must
//     attribute facts to a session and a date, because two sessions months apart
//     routinely contradict each other and the newer one usually wins.
const (
	defaultProjectLimit         = 60
	defaultProjectPerSessionCap = 8
	defaultProjectMaxRounds     = 2
	defaultProjectThreshold     = 0.7
	defaultProjectHalfLifeDays  = 45.0
	defaultProjectMaxChars      = 18000
	defaultProjectMaxTopics     = 40
	minProjectCosine            = 0.1
	projectRecencyWeight        = 0.15
)

// ProjectRecallOptions tunes a project-scoped recall.
type ProjectRecallOptions struct {
	ProjectID string
	Question  string

	Limit         int     // max semantically matched facts fed to synthesis
	PerSessionCap int     // max matched facts contributed by any one session
	MaxRounds     int     // max refinement rounds
	Threshold     float32 // confidence threshold to stop early
	HalfLifeDays  float64 // recency half-life for the score boost
	MaxChars      int     // char budget for the facts block

	Since          int64    // unix ms; 0 = no lower bound
	Until          int64    // unix ms; 0 = no upper bound
	SessionTypes   []string // empty = every session type
	ExcludeSession string   // session to skip, usually the caller's own
	OnlySession    string   // restrict to one conversation; overrides SessionTypes
	TopicName      string   // restrict to one topic

	// Chat is the synthesis LLM, built from the session's selected model. When
	// nil, recall returns the assembled context without synthesis.
	Chat ChatClient
}

func (o *ProjectRecallOptions) applyDefaults() {
	if o.Limit <= 0 {
		o.Limit = defaultProjectLimit
	}
	if o.PerSessionCap <= 0 {
		o.PerSessionCap = defaultProjectPerSessionCap
	}
	if o.MaxRounds <= 0 {
		o.MaxRounds = defaultProjectMaxRounds
	}
	if o.Threshold <= 0 {
		o.Threshold = defaultProjectThreshold
	}
	if o.HalfLifeDays <= 0 {
		o.HalfLifeDays = defaultProjectHalfLifeDays
	}
	if o.MaxChars <= 0 {
		o.MaxChars = defaultProjectMaxChars
	}
}

func (o ProjectRecallOptions) filter() ProjectFilter {
	return ProjectFilter{
		Since:          o.Since,
		Until:          o.Until,
		SessionTypes:   o.SessionTypes,
		TopicName:      o.TopicName,
		ExcludeSession: o.ExcludeSession,
		OnlySession:    o.OnlySession,
	}
}

// ProjectRecallResult is the outcome of a project-scoped recall.
type ProjectRecallResult struct {
	Answer       string
	Confidence   float32
	Rounds       int
	FactsUsed    int
	SessionsUsed int
	TotalFacts   int
	TotalTopics  int
}

// topicStat aggregates one topic across every session in the project.
type topicStat struct {
	Name     string
	Facts    int
	First    int64
	Last     int64
	Sessions map[string]bool
	Labels   map[string]int
}

// scoredFact is a fact with its retrieval score.
type scoredFact struct {
	node  Node
	score float32
}

// ProjectRecall runs semantic recall across every session in a project.
func (g *Graph) ProjectRecall(ctx context.Context, opts ProjectRecallOptions) (*ProjectRecallResult, error) {
	opts.applyDefaults()
	if opts.ProjectID == "" {
		return nil, fmt.Errorf("ProjectRecall: projectID is required")
	}
	if g.Embed == nil {
		return nil, fmt.Errorf("ProjectRecall: embedder is required for agentic memory")
	}

	var queryVec []float32
	if vecs, err := g.Embed.Embed(ctx, []string{opts.Question}); err == nil && len(vecs) > 0 {
		queryVec = vecs[0]
	} else if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}

	matches, stats, totalFacts, err := g.scanProject(opts, queryVec)
	if err != nil {
		return nil, err
	}
	if totalFacts == 0 {
		return &ProjectRecallResult{}, nil
	}

	facts, sessionCount, err := g.expandWithNeighbours(matches, opts)
	if err != nil {
		return nil, err
	}

	names, err := g.Store.ProjectSessionNames(opts.ProjectID)
	if err != nil {
		slog.Warn("project recall: session names unavailable", "err", err)
		names = map[string]string{}
	}

	matchedKeys := make(map[string]bool, len(matches))
	for _, m := range matches {
		matchedKeys[m.node.Key] = true
	}

	skeleton := projectSkeletonText(stats, defaultProjectMaxTopics, totalFacts, sessionCount)
	factsBlock := projectFactsText(facts, matchedKeys, names, opts.MaxChars)

	result := &ProjectRecallResult{
		FactsUsed:    len(matches),
		SessionsUsed: sessionCount,
		TotalFacts:   totalFacts,
		TotalTopics:  len(stats),
	}

	// No synthesis LLM: hand back the assembled context directly. It is already
	// grouped by session and chronologically ordered, so it is usable as-is.
	if opts.Chat == nil {
		result.Answer = skeleton + "\n" + factsBlock
		return result, nil
	}

	var (
		round      int
		confidence float32 = 1.0
		best       string
		history    []string
	)
	for round < opts.MaxRounds {
		round++
		prompt := buildProjectRecallPrompt(opts.Question, skeleton, factsBlock, history)

		resp, err := opts.Chat.Chat(ctx, "", prompt)
		if err != nil {
			return nil, fmt.Errorf("project recall round %d: %w", round, err)
		}
		parsed := parseRecallResponse(resp)

		if !parsed.ContextFound || parsed.FinalContext == "EMPTY_CONTEXT" {
			result.Rounds = round
			result.Confidence = 1.0
			return result, nil
		}

		best = parsed.FinalContext
		confidence = parsed.Confidence

		if (!parsed.RefinementNeeded && parsed.Confidence >= opts.Threshold) || round >= opts.MaxRounds {
			break
		}

		if parsed.FinalContext != "" {
			history = append(history, fmt.Sprintf("Round %d FINAL_CONTEXT: %s", round, parsed.FinalContext))
		}
		if parsed.Critique != "" {
			history = append(history, fmt.Sprintf("Round %d CRITIQUE: %s", round, parsed.Critique))
			history = append(history, "Instruction for next round: rewrite FINAL_CONTEXT fixing every issue in the CRITIQUE, keeping the facts that were correct.")
		}

		// A follow-up re-queries the project with a sharper phrasing and merges
		// whatever is new into the fact block, so the next round sees strictly
		// more than this one did.
		if parsed.FollowUp != "" {
			if fvecs, err := g.Embed.Embed(ctx, []string{parsed.FollowUp}); err == nil && len(fvecs) > 0 {
				extra, _, _, err := g.scanProject(opts, fvecs[0])
				if err == nil && len(extra) > 0 {
					seen := make(map[string]bool, len(facts))
					for _, f := range facts {
						seen[f.Key] = true
					}
					var added int
					for _, e := range extra {
						if !seen[e.node.Key] {
							facts = append(facts, e.node)
							matchedKeys[e.node.Key] = true
							seen[e.node.Key] = true
							added++
						}
					}
					if added > 0 {
						factsBlock = projectFactsText(facts, matchedKeys, names, opts.MaxChars)
						result.FactsUsed += added
					}
				}
			}
			history = append(history, fmt.Sprintf("Round %d — follow-up searched: %s", round, parsed.FollowUp))
		}
	}

	result.Answer = best
	result.Confidence = confidence
	result.Rounds = round
	return result, nil
}

// scanProject makes one pass over the project's facts, simultaneously selecting
// the top matches and aggregating the topic map. Both outputs come from the same
// scan because the pass is the expensive part: re-reading every row to build the
// map separately would double the cost of every recall.
func (g *Graph) scanProject(opts ProjectRecallOptions, queryVec []float32) ([]scoredFact, map[string]*topicStat, int, error) {
	stats := make(map[string]*topicStat)
	perSession := make(map[string][]scoredFact)
	now := time.Now().UnixMilli()
	total := 0

	start := time.Now()
	err := g.Store.ScanProjectFacts(opts.ProjectID, opts.filter(), func(n Node, emb []float32) {
		total++

		st := stats[n.TopicName]
		if st == nil {
			st = &topicStat{
				Name:     n.TopicName,
				First:    n.CreatedAt,
				Last:     n.CreatedAt,
				Sessions: map[string]bool{},
				Labels:   map[string]int{},
			}
			stats[n.TopicName] = st
		}
		st.Facts++
		st.Sessions[n.SessionID] = true
		if n.CreatedAt < st.First {
			st.First = n.CreatedAt
		}
		if n.CreatedAt > st.Last {
			st.Last = n.CreatedAt
		}
		for _, l := range n.Labels {
			st.Labels[l]++
		}

		if len(emb) == 0 || len(queryVec) == 0 {
			return
		}
		base := cosine(queryVec, emb)
		if base < minProjectCosine {
			return
		}
		score := base + projectRecencyWeight*recencyDecay(n.CreatedAt, now, opts.HalfLifeDays)

		// Cap per session before the global cut. Without it a single 400-turn
		// session can fill every slot and the answer silently loses the rest of
		// the project's history.
		bucket := perSession[n.SessionID]
		if len(bucket) < opts.PerSessionCap {
			perSession[n.SessionID] = append(bucket, scoredFact{node: n, score: score})
			return
		}
		weakest := 0
		for i := 1; i < len(bucket); i++ {
			if bucket[i].score < bucket[weakest].score {
				weakest = i
			}
		}
		if score > bucket[weakest].score {
			bucket[weakest] = scoredFact{node: n, score: score}
		}
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("scan project facts: %w", err)
	}

	var matches []scoredFact
	for _, bucket := range perSession {
		matches = append(matches, bucket...)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}

	slog.Info("project recall scan",
		"project", opts.ProjectID,
		"facts_scanned", total,
		"sessions_matched", len(perSession),
		"matches", len(matches),
		"duration", time.Since(start),
	)
	return matches, stats, total, nil
}

// recencyDecay returns 1.0 for a fact written now, decaying by half every
// halfLifeDays. Project memory spans months, so an exact-similarity tie between
// a decision from last week and one from last spring should favour last week.
func recencyDecay(createdAt, now int64, halfLifeDays float64) float32 {
	if createdAt <= 0 || halfLifeDays <= 0 {
		return 0
	}
	ageDays := float64(now-createdAt) / float64(24*time.Hour/time.Millisecond)
	if ageDays < 0 {
		ageDays = 0
	}
	return float32(math.Exp(-math.Ln2 * ageDays / halfLifeDays))
}

// expandWithNeighbours pulls the turns immediately before and after each match.
// Adjacency is only meaningful inside one session — "order" restarts at 1 in
// every conversation — so the lookup is grouped by session rather than run
// against the project as a whole.
func (g *Graph) expandWithNeighbours(matches []scoredFact, opts ProjectRecallOptions) ([]Node, int, error) {
	ordersBySession := make(map[string]map[int]bool)
	for _, m := range matches {
		if ordersBySession[m.node.SessionID] == nil {
			ordersBySession[m.node.SessionID] = map[int]bool{}
		}
		ordersBySession[m.node.SessionID][m.node.Order] = true
		ordersBySession[m.node.SessionID][m.node.Order-1] = true
		ordersBySession[m.node.SessionID][m.node.Order+1] = true
	}

	seen := make(map[string]bool, len(matches))
	var out []Node
	for sessionID, orderSet := range ordersBySession {
		orders := make([]int, 0, len(orderSet))
		for o := range orderSet {
			if o > 0 {
				orders = append(orders, o)
			}
		}
		nodes, err := g.Store.ProjectFactsAt(opts.ProjectID, sessionID, orders, opts.filter())
		if err != nil {
			return nil, 0, fmt.Errorf("neighbour lookup: %w", err)
		}
		for _, n := range nodes {
			if !seen[n.Key] {
				seen[n.Key] = true
				out = append(out, n)
			}
		}
	}

	// A match whose neighbour query returned nothing (deleted row, filter edge)
	// must still reach the prompt — it is the reason this recall ran.
	for _, m := range matches {
		if !seen[m.node.Key] {
			seen[m.node.Key] = true
			out = append(out, m.node)
		}
	}
	return out, len(ordersBySession), nil
}

// projectSkeletonText renders the bird's-eye map of the project: what topics
// exist, how big they are, and when they were active. This replaces the
// session tree's full fact dump, which would be far too large at project scale.
func projectSkeletonText(stats map[string]*topicStat, maxTopics, totalFacts, sessionCount int) string {
	ordered := make([]*topicStat, 0, len(stats))
	for _, st := range stats {
		ordered = append(ordered, st)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Facts != ordered[j].Facts {
			return ordered[i].Facts > ordered[j].Facts
		}
		return ordered[i].Last > ordered[j].Last
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== PROJECT MAP (%d facts · %d topics · %d conversations matched) ===\n",
		totalFacts, len(stats), sessionCount)

	shown := ordered
	if maxTopics > 0 && len(shown) > maxTopics {
		shown = shown[:maxTopics]
	}
	for _, st := range shown {
		name := st.Name
		if name == "" {
			name = "(untitled)"
		}
		fmt.Fprintf(&sb, "Topic: %s [%d facts · %s → %s · %d conversations]\n",
			name, st.Facts, formatDay(st.First), formatDay(st.Last), len(st.Sessions))
		if labels := topLabels(st.Labels, 8); len(labels) > 0 {
			sb.WriteString("  labels: " + strings.Join(labels, ", ") + "\n")
		}
	}
	if len(ordered) > len(shown) {
		fmt.Fprintf(&sb, "(+%d smaller topics not listed — ask a follow-up to search them)\n", len(ordered)-len(shown))
	}
	return sb.String()
}

// topLabels returns the n most frequent labels, ties broken alphabetically so
// the output is stable across calls.
func topLabels(counts map[string]int, n int) []string {
	if len(counts) == 0 {
		return nil
	}
	labels := make([]string, 0, len(counts))
	for l := range counts {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if counts[labels[i]] != counts[labels[j]] {
			return counts[labels[i]] > counts[labels[j]]
		}
		return labels[i] < labels[j]
	})
	if len(labels) > n {
		labels = labels[:n]
	}
	return labels
}

// projectFactsText renders the retrieved facts grouped by the conversation they
// came from, oldest conversation first. Attribution is not decoration here: the
// synthesis step is asked to resolve contradictions by date, which it can only
// do if every fact says where and when it came from.
//
// The block is fitted to maxChars by shrinking per-fact text first and dropping
// context-only neighbours second. Matched facts are never dropped silently — if
// the budget still cannot hold them, the block says how many were omitted.
func projectFactsText(facts []Node, matchedKeys map[string]bool, names map[string]string, maxChars int) string {
	for _, factLimit := range []int{1200, 600, 300} {
		if out := renderProjectFacts(facts, matchedKeys, names, factLimit, 0); len(out) <= maxChars {
			return out
		}
	}
	// Still over budget with the tightest per-fact limit: start dropping the
	// neighbour turns, which exist only to frame the matches.
	for _, dropNeighbours := range []int{1, 2} {
		if out := renderProjectFacts(facts, matchedKeys, names, 300, dropNeighbours); len(out) <= maxChars {
			return out
		}
	}
	out := renderProjectFacts(facts, matchedKeys, names, 300, 2)
	if len(out) > maxChars {
		slog.Warn("project recall: fact block exceeds budget after trimming", "chars", len(out), "budget", maxChars)
		out = out[:maxChars] + "\n… (truncated to fit the context budget — narrow the question or add a date range)\n"
	}
	return out
}

// renderProjectFacts builds the fact block. dropLevel 1 omits neighbour turns,
// 2 also collapses matched facts to their one-line summary.
func renderProjectFacts(facts []Node, matchedKeys map[string]bool, names map[string]string, factLimit, dropLevel int) string {
	bySession := make(map[string][]Node)
	for _, f := range facts {
		if dropLevel >= 1 && !matchedKeys[f.Key] {
			continue
		}
		bySession[f.SessionID] = append(bySession[f.SessionID], f)
	}

	sessions := make([]string, 0, len(bySession))
	for id := range bySession {
		sessions = append(sessions, id)
		sort.Slice(bySession[id], func(i, j int) bool { return bySession[id][i].Order < bySession[id][j].Order })
	}
	// Oldest conversation first: the synthesis prompt asks for a chronological
	// narrative, so the raw material arrives in that order too.
	sort.Slice(sessions, func(i, j int) bool {
		return firstCreatedAt(bySession[sessions[i]]) < firstCreatedAt(bySession[sessions[j]])
	})

	var sb strings.Builder
	sb.WriteString("=== RELEVANT FACTS (★ = semantic match; unmarked lines are adjacent turns) ===\n")
	for _, id := range sessions {
		nodes := bySession[id]
		name := names[id]
		if name == "" {
			name = "untitled conversation " + shortID(id)
		}
		fmt.Fprintf(&sb, "\n── %s · %s → %s ──\n", name,
			formatDay(firstCreatedAt(nodes)), formatDay(lastCreatedAt(nodes)))

		for _, f := range nodes {
			marker := "  "
			if matchedKeys[f.Key] {
				marker = "★ "
			}
			// Neighbours and heavily-trimmed matches collapse to the LLM-written
			// summary; it is what the summary exists for.
			if !matchedKeys[f.Key] || dropLevel >= 2 {
				text := f.Summary
				if text == "" {
					text = truncate(f.Content, 200)
				}
				fmt.Fprintf(&sb, "%s[%s] %s\n", marker, formatDay(f.CreatedAt), collapse(text))
				continue
			}
			if f.Question != "" {
				fmt.Fprintf(&sb, "%s[%s] Q: %s\n    A: %s\n", marker, formatDay(f.CreatedAt),
					collapse(truncate(f.Question, 300)), collapse(factDisplayText(f, factLimit)))
			} else {
				fmt.Fprintf(&sb, "%s[%s] %s\n", marker, formatDay(f.CreatedAt), collapse(factDisplayText(f, factLimit)))
			}
		}
	}

	if dropLevel >= 1 {
		var dropped int
		for _, f := range facts {
			if !matchedKeys[f.Key] {
				dropped++
			}
		}
		if dropped > 0 {
			fmt.Fprintf(&sb, "\n(%d adjacent context turns omitted to fit the context budget)\n", dropped)
		}
	}
	return sb.String()
}

func firstCreatedAt(nodes []Node) int64 {
	var min int64
	for i, n := range nodes {
		if i == 0 || n.CreatedAt < min {
			min = n.CreatedAt
		}
	}
	return min
}

func lastCreatedAt(nodes []Node) int64 {
	var max int64
	for _, n := range nodes {
		if n.CreatedAt > max {
			max = n.CreatedAt
		}
	}
	return max
}

func formatDay(ms int64) string {
	if ms <= 0 {
		return "undated"
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}

// collapse flattens a fact onto one line. Stored responses are multi-line tool
// traces, and their blank lines would otherwise dominate the prompt.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// buildProjectRecallPrompt frames the project map and the matched facts for the
// synthesis LLM.
func buildProjectRecallPrompt(question, skeleton, facts string, history []string) string {
	var sb strings.Builder
	sb.WriteString("You are a Context Enricher working over the ENTIRE history of a software project —\n")
	sb.WriteString("every past conversation held in this workspace, not just the current one.\n")
	sb.WriteString("Your sole job: given an incoming query, produce a tight background context block\n")
	sb.WriteString("that frames the query for a downstream LLM.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- NEVER answer the query. Surface context around it, not a response to it.\n")
	sb.WriteString("- Be specific and to the point. No preamble, no prose filler, no repetition.\n")
	sb.WriteString("- Use bullet points. Cover all relevant facts — do not artificially limit — but omit everything irrelevant.\n")
	sb.WriteString("- Every fact is dated and attributed to the conversation it came from. Carry that\n")
	sb.WriteString("  attribution into your bullets when it matters (e.g. \"decided 2026-05-02\").\n")
	sb.WriteString("- Order bullets chronologically (oldest → newest) so the downstream LLM sees how the\n")
	sb.WriteString("  project evolved, grouping under short timeline labels where it aids clarity.\n")
	sb.WriteString("- CONFLICTS ARE EXPECTED across months of history. When two facts disagree, lead with\n")
	sb.WriteString("  the most recent one and say explicitly that it superseded the earlier one, with both\n")
	sb.WriteString("  dates. Never silently drop the older fact and never present a stale decision as current.\n")
	sb.WriteString("- Facts marked ★ matched the query semantically; unmarked lines are the adjacent turns,\n")
	sb.WriteString("  included only as context for the marked ones.\n\n")

	if len(history) > 0 {
		sb.WriteString("Previous retrieval rounds:\n")
		for _, h := range history {
			sb.WriteString("  " + h + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(skeleton)
	sb.WriteString("\n")
	sb.WriteString(facts)
	sb.WriteString("\nQuery: " + question + "\n\n")
	sb.WriteString(`Respond with a single JSON object, no markdown fences:
{
  "context_found": <true|false — false if the project history has nothing relevant to this query>,
  "critique": "<Did you accidentally answer the query? Include irrelevant facts? Miss a supersession between an old and a new fact? Empty if no issues.>",
  "refinement_needed": <true|false — true if the critique found issues or another search of the project would help>,
  "confidence": <0.0-1.0>,
  "followup": "<specific topic to search across the project next if refinement_needed, else empty>",
  "facts_used": <integer>,
  "final_context": "<Chronologically ordered bullet-point context block (• prefix) covering all relevant project background, with dates and supersessions made explicit. Empty string if context_found is false.>"
}`)
	return sb.String()
}
