package memfile

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// The ranking recipe's numbers. They were measured against this project's live
// corpus (1,552 turn summaries over 445 sessions) and are deliberately round: 20
// sweeps of them moved mixed rank-1 only inside 56.4–67.4, and every "rescale the
// bonuses to bm25's scale" variant collapsed to 58.0. They encode an order —
// body, then labels, title, breadth, recency — so do not rescale one without
// re-measuring all of them against the live corpus.
const (
	labelAllBonus  = 60 // every query term appears in one document's Topics: labels
	labelSomeBonus = 40 // some do
	titleAllBonus  = 100
	titleSomeBonus = 50
	// Breadth is capped at five turns: it stops a 64-turn conversation from
	// outranking a precise 2-turn one on volume alone. The cap is not dedupe —
	// neighbouring turns' labels barely overlap (mean Jaccard 0.03).
	breadthBonus   = 4
	breadthTurnCap = 5
	recencyMax     = 10.0
	recencyDays    = 30.0

	// A plural query's singular fallback is the same evidence at a discount, not
	// extra evidence, hence a weight and never a sum. The gate keeps it from
	// reordering a query the index already answers precisely.
	pluralGateSessions = 10
	pluralWeight       = 0.35

	maxQueryTerms = 6
	hitTurnCount  = 3
	hitLabelCap   = 8
)

// rankRow is one indexed document that matched at least one query term, with the
// best score it earned for each term. Scores is keyed by the term's index in the
// query: a document matched by two terms carries two entries.
type rankRow struct {
	RowID     int64
	SessionID string
	Kind      string
	Title     string
	Labels    []string
	CreatedAt int64 // unix millis
	Snippet   string
	Scores    map[int]float64
}

// sessionMeta is what the session table says about a hit's conversation. It is
// the only source of title, model and timestamp a hit renders from, which is also
// why a row whose conversation is gone can never become a hit.
type sessionMeta struct {
	Title     string
	Model     string
	UpdatedAt int64 // unix millis
}

// termRow is one document returned by one term's MATCH.
type termRow struct {
	rowid        int64
	sessionID    string
	kind         string
	title        string
	labels       []string
	createdAt    int64
	snippet      string
	sessionTitle string
	model        string
	updatedAt    int64
	score        float64
}

// rankHits turns matched per-turn rows into ranked per-session hits.
//
// It is pure — no database, no clock — so the order it produces is pinned by a
// table test rather than inferred from a live corpus. rows are every document
// that matched any term; sessions is the metadata of the conversations those rows
// belong to (a row absent from it is a ghost and is dropped).
func rankHits(rows []rankRow, sessions map[string]sessionMeta, terms []string, now time.Time) []Hit {
	hits := make([]Hit, 0, len(sessions))
	if len(terms) == 0 {
		return hits
	}

	grouped := make(map[string][]rankRow, len(sessions))
	for _, row := range rows {
		if _, ok := sessions[row.SessionID]; !ok {
			continue // no conversation behind it: never surface a ghost
		}
		grouped[row.SessionID] = append(grouped[row.SessionID], row)
	}

	for sessionID, docs := range grouped {
		meta := sessions[sessionID]

		// The union rule: a session qualifies when every term matched in SOME
		// turn. This is what a single multi-term MATCH cannot do, and it is why
		// cross-turn queries went from 9/60 found to 57/60.
		covered := make(map[int]struct{}, len(terms))
		for _, doc := range docs {
			for term := range doc.Scores {
				covered[term] = struct{}{}
			}
		}
		if len(covered) < len(terms) {
			continue
		}

		// One best turn, not a sum: a conversation is "about" something because of
		// its strongest turn, and summing every turn let long sessions win on
		// volume. Coverage weighting keeps a turn that matched 2 of 3 terms from
		// earning full body credit.
		best := docs[0]
		for _, doc := range docs[1:] {
			if betterTurn(doc, best) {
				best = doc
			}
		}
		bestScore, bestCovered := turnTotals(best)
		score := bestScore * float64(bestCovered) / float64(len(terms))

		// Label and title bonuses float free across the session's matching
		// documents rather than riding the best-body turn (66.4 mixed when tied to
		// the best row, 67.8 when they float), and are simpler to compute.
		labelTier := 0
		for _, doc := range docs {
			if tier := labelsTier(terms, doc.Labels); tier > labelTier {
				labelTier = tier
			}
		}
		switch labelTier {
		case 2:
			score += labelAllBonus
		case 1:
			score += labelSomeBonus
		}

		titles := make([]string, 0, len(docs)+1)
		titles = append(titles, meta.Title)
		for _, doc := range docs {
			titles = append(titles, doc.Title)
		}
		titleTier := titlesTier(terms, titles)
		switch {
		// A single-term query has no "every term" tier: one word in the title is
		// the "some" bonus, or a title match would outweigh a perfect body match.
		case len(terms) > 1 && titleTier == 2:
			score += titleAllBonus
		case titleTier > 0:
			score += titleSomeBonus
		}

		turnDocs := matchingTurns(docs)
		breadth := len(turnDocs)
		if breadth > breadthTurnCap {
			breadth = breadthTurnCap
		}
		score += float64(breadthBonus * breadth)
		score += recencyBonus(now, meta.UpdatedAt)

		top := bestTurns(docs, hitTurnCount)
		matchedTurns := len(turnDocs)
		if matchedTurns == 0 {
			// Only a conversation-level document matched (its title or its
			// compaction summary); it is still one turn's worth of answer.
			matchedTurns = len(docs)
		}
		snippet := best.Snippet
		if snippet == "" && len(top) > 0 {
			snippet = top[0].Snippet
		}

		source := kindTurn
		if best.Kind != kindTurn {
			source = kindMemory
		}
		if titleTier > 0 {
			source = "title"
		}

		turns := make([]HitTurn, 0, len(top))
		for _, doc := range top {
			turns = append(turns, HitTurn{At: doc.CreatedAt, Labels: doc.Labels, Snippet: doc.Snippet})
		}
		hits = append(hits, Hit{
			SessionID:    sessionID,
			Title:        firstNonEmpty(meta.Title, best.Title),
			Model:        meta.Model,
			UpdatedAt:    meta.UpdatedAt,
			Score:        score,
			MatchedTurns: matchedTurns,
			Source:       source,
			Labels:       unionLabels(top, hitLabelCap),
			Snippet:      snippet,
			Turns:        turns,
		})
	}

	hitsByScoreDesc(hits)
	return hits
}

// turnTotals is a document's summed score and the number of distinct query terms
// it covers.
func turnTotals(doc rankRow) (float64, int) {
	sum := 0.0
	for _, score := range doc.Scores {
		sum += score
	}
	return sum, len(doc.Scores)
}

// betterTurn reports whether a should be preferred to b as a session's
// best-matching turn: most distinct terms, then the highest summed score, then
// the most recent, then a stable id so equal rows never swap between runs.
func betterTurn(a, b rankRow) bool {
	aSum, aCovered := turnTotals(a)
	bSum, bCovered := turnTotals(b)
	switch {
	case aCovered != bCovered:
		return aCovered > bCovered
	case aSum != bSum:
		return aSum > bSum
	case a.CreatedAt != b.CreatedAt:
		return a.CreatedAt > b.CreatedAt
	}
	return a.RowID < b.RowID
}

// bestTurns returns up to n of a session's documents, best first.
func bestTurns(docs []rankRow, n int) []rankRow {
	sorted := append([]rankRow(nil), docs...)
	sort.SliceStable(sorted, func(i, j int) bool { return betterTurn(sorted[i], sorted[j]) })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// matchingTurns are the documents that count toward breadth: the per-turn
// summaries and the conversation's curated memory file. A 'session' document is
// the same conversation's title and summary, so counting it as a turn would add
// a constant to every conversation's breadth and dilute the signal.
func matchingTurns(docs []rankRow) []rankRow {
	out := make([]rankRow, 0, len(docs))
	for _, doc := range docs {
		if doc.Kind == kindTurn || doc.Kind == kindMemory {
			out = append(out, doc)
		}
	}
	return out
}

// unionLabels is the union of a set of documents' topic labels, ordered by how
// many of them carry the label (first spelling wins), capped.
func unionLabels(docs []rankRow, limit int) []string {
	var order []string
	count := make(map[string]int)
	spelling := make(map[string]string)
	for _, doc := range docs {
		for _, label := range doc.Labels {
			key := strings.ToLower(strings.TrimSpace(label))
			if key == "" {
				continue
			}
			if _, seen := count[key]; !seen {
				order = append(order, key)
				spelling[key] = strings.TrimSpace(label)
			}
			count[key]++
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	if len(order) > limit {
		order = order[:limit]
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, spelling[key])
	}
	return out
}

// recencyBonus decays a conversation's freshness over the last 30 days. A
// 30-point recency term scored worse (64.8 against 66.2): a stale but precise
// answer still beats a fresh vague one.
func recencyBonus(now time.Time, updatedAtMillis int64) float64 {
	if updatedAtMillis <= 0 {
		return 0
	}
	age := now.Sub(time.UnixMilli(updatedAtMillis)).Hours() / 24
	if age < 0 {
		age = 0
	}
	if age >= recencyDays {
		return 0
	}
	return recencyMax * (1 - age/recencyDays)
}

// labelsTier scores how many of terms appear in one document's topic labels:
// 2 = all, 1 = some, 0 = none.
func labelsTier(terms, labels []string) int {
	found := 0
	for _, term := range terms {
		for _, label := range labels {
			if wordPrefixPresent(label, term) {
				found++
				break
			}
		}
	}
	return tierOf(found, len(terms))
}

// titlesTier is labelsTier over a set of candidate titles (the session's own and
// its documents'), taking the best one.
func titlesTier(terms, titles []string) int {
	best := 0
	for _, title := range titles {
		found := 0
		for _, term := range terms {
			if wordPrefixPresent(title, term) {
				found++
			}
		}
		if tier := tierOf(found, len(terms)); tier > best {
			best = tier
		}
	}
	return best
}

func tierOf(found, total int) int {
	switch {
	case total > 0 && found == total:
		return 2
	case found > 0:
		return 1
	}
	return 0
}

// wordPrefixPresent reports whether text contains a word starting with term. The
// match is a word-prefix one, the same shape the FTS query asks for, so a term
// that earned a bonus is a term the index also matched.
func wordPrefixPresent(text, term string) bool {
	for _, word := range splitWords(text) {
		if strings.HasPrefix(word, term) {
			return true
		}
	}
	return false
}

// splitWords lowercases text and splits it on everything that is not a letter or
// digit. That is what unicode61 does, with one deliberate similarity: a camelCase
// identifier stays one word ("previewHostDispatch"), so prefix-matching it is
// nearly exact.
func splitWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// queryStopWords are the words a person types but never means. This is not a
// general English list: a query is two or three words, so it only has to remove
// what would otherwise become a term that matches half the corpus.
var queryStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "been": {},
	"but": {}, "by": {}, "can": {}, "could": {}, "did": {}, "do": {}, "does": {},
	"for": {}, "from": {}, "had": {}, "has": {}, "have": {}, "how": {}, "if": {},
	"in": {}, "into": {}, "is": {}, "it": {}, "its": {}, "me": {}, "my": {}, "no": {},
	"not": {}, "of": {}, "on": {}, "or": {}, "our": {}, "out": {}, "so": {}, "that": {},
	"the": {}, "their": {}, "then": {}, "there": {}, "these": {}, "they": {}, "this": {},
	"to": {}, "up": {}, "us": {}, "was": {}, "we": {}, "were": {}, "what": {}, "when": {},
	"where": {}, "which": {}, "who": {}, "why": {}, "will": {}, "with": {}, "would": {},
	"you": {}, "your": {},
}

// tokenizeQuery turns a typed query into the terms the index is searched for:
// lowercase, split on non-alphanumerics, one-character terms and stopwords
// dropped, duplicates collapsed, at most six terms.
func tokenizeQuery(query string) []string {
	terms := make([]string, 0, maxQueryTerms)
	seen := make(map[string]struct{}, maxQueryTerms)
	for _, word := range splitWords(query) {
		if len([]rune(word)) < 2 {
			continue
		}
		if _, stop := queryStopWords[word]; stop {
			continue
		}
		if _, dup := seen[word]; dup {
			continue
		}
		seen[word] = struct{}{}
		terms = append(terms, word)
		if len(terms) == maxQueryTerms {
			break
		}
	}
	return terms
}

// ftsExpr is one term's fts5 expression: a quoted phrase with a prefix marker, so
// "auth" matches authorization as well as auth. The quote escape cannot fire on a
// tokenized term; it is here so a future caller that passes raw text stays safe.
func ftsExpr(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"*`
}

// singularForm strips a query term down to the singular a document is likely to
// spell it in, or returns "" when no plausible singular exists. It is a query-side
// fallback only — nothing is stemmed in the index.
func singularForm(term string) string {
	if len(term) < 4 {
		return ""
	}
	switch {
	case strings.HasSuffix(term, "ies"):
		return term[:len(term)-3] + "y"
	case strings.HasSuffix(term, "sses"), strings.HasSuffix(term, "shes"),
		strings.HasSuffix(term, "ches"), strings.HasSuffix(term, "xes"),
		strings.HasSuffix(term, "zes"):
		return term[:len(term)-2]
	case strings.HasSuffix(term, "ss"), strings.HasSuffix(term, "us"), strings.HasSuffix(term, "is"):
		// "class", "status", "analysis": the trailing s is not a plural marker.
		return ""
	case strings.HasSuffix(term, "s"):
		return term[:len(term)-1]
	}
	return ""
}
