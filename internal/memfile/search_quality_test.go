package memfile

// TestSearchQuality is the ranking eval harness: the regression gate for every
// change to the session-search ranking. It builds five query sets from a real
// memory corpus, indexes that corpus once into a throwaway database, and scores
// rank-1 / recall@5 / MRR per set plus a mixed total.
//
// It is skipped unless OGCODE_SEARCH_EVAL_DIR names a memory corpus directory —
// the layout memfile writes: <dir>/<ts>--<tag>--<slug>.md turn files, optionally
// with <dir>/sessions/<key>.md below it. Run it against this project's own
// corpus with:
//
//	OGCODE_SEARCH_EVAL_DIR=$PWD/.ogcode/memory go test ./internal/memfile/ -run TestSearchQuality -v
//
// The floors in evalFloors are quoted from the scoreboard this harness itself
// prints (set below what a clean corpus measured on 2026-11-01, because the
// queries here are built deterministically from the corpus rather than sampled
// by a probe script). Re-run the command above after a ranking change and diff
// the table; the floors are the gate, the table is the story.
//
// The sets are reproducible: the corpus is walked in path order, words are ranked by
// (document frequency, term), candidate queries are deduped and sorted by query text,
// and each set takes N evenly spaced entries. The logged fingerprint hashes the graded
// queries, so a corpus or set-construction change is visible in the output.
//
// Set OGCODE_SEARCH_EVAL_DEBUG=N (or "all") to print the queries that missed, with the
// hits that outranked the expected session.
//
import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

const (
	// evalEnvDir names the memory corpus to score. Unset means skip.
	evalEnvDir = "OGCODE_SEARCH_EVAL_DIR"

	// evalEnvDebug makes the test print the queries it got wrong, with the
	// hits it got instead: N per set when set to a number, every miss when set
	// to "all". A floor breach with no example is a number nobody can act on.
	evalEnvDebug = "OGCODE_SEARCH_EVAL_DEBUG"

	// evalSearchLimit is the depth Search returns per query. Rank-1 and r@5 are
	// read off it; "found" (the metric the plan reports for cross-turn queries)
	// means anywhere in this window.
	evalSearchLimit = 10
	evalTop5        = 5

	// evalRareDFMax is the document-frequency ceiling for a "rare word": the plan
	// builds the body and cross-turn sets from words appearing in at most this
	// many turns. Words in exactly one turn are not search problems, so the band
	// starts at 2.
	evalRareDFMin = 2
	evalRareDFMax = 25

	// Set sizes, straight from the plan's Accuracy harness.
	evalLabelQueries     = 250
	evalBodyQueries      = 150
	evalTitleQueries     = 100
	evalPluralQueries    = 60
	evalCrossTurnQueries = 60
)

// evalSetOrder fixes the order the scoreboard prints in — the plan's table order.
var evalSetOrder = []string{"label", "body", "title", "plural", "cross"}

// evalStopwords is the tokenizer's stoplist. It mirrors the ranking tokenizer's
// intent (drop the words that carry no topical signal) without depending on its
// unexported copy: the eval only needs a stable definition, not the same one.
var evalStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {}, "then": {},
	"than": {}, "from": {}, "was": {}, "were": {}, "are": {}, "but": {}, "not": {},
	"you": {}, "your": {}, "its": {}, "into": {}, "out": {}, "all": {}, "any": {},
	"can": {}, "had": {}, "has": {}, "have": {}, "how": {}, "our": {}, "who": {},
	"why": {}, "when": {}, "what": {}, "which": {}, "will": {}, "would": {}, "there": {},
	"their": {}, "them": {}, "they": {}, "been": {}, "being": {}, "after": {}, "before": {},
	"over": {}, "under": {}, "about": {}, "also": {}, "just": {}, "only": {}, "some": {},
	"such": {}, "very": {}, "more": {}, "most": {}, "now": {}, "one": {}, "two": {},
	"per": {}, "via": {}, "yet": {}, "did": {}, "does": {}, "done": {}, "each": {},
	"here": {}, "his": {}, "her": {}, "she": {}, "him": {}, "may": {}, "might": {},
	"must": {}, "should": {}, "still": {}, "though": {}, "while": {}, "without": {},
}

// evalTurn is one summary file, parsed the way the indexer sees it.
type evalTurn struct {
	rel       string // slash path relative to the corpus dir, the stable sort key
	sessionID string
	projectID string
	title     string
	labels    []string            // first `Topics:` line, via the indexer's own parser
	terms     map[string]struct{} // body terms
}

// evalCorpus is the whole memory folder, loaded once.
type evalCorpus struct {
	turns      []*evalTurn            // sorted by rel
	sessions   map[string][]*evalTurn // session id -> its turns, sorted by rel
	sessionIDs []string               // session ids, sorted
	projectID  string                 // the corpus's own project id (modal frontmatter value)
	skipped    int                    // files without a session attribution
}

// evalCase is one graded query: the text, and the session it should find.
type evalCase struct {
	query     string
	sessionID string
}

// evalTally accumulates one set's score (or the mixed total).
type evalTally struct {
	n, rank1, r5, found int
	mrr                 float64
}

func (t *evalTally) add(rank int) {
	t.n++
	if rank == 1 {
		t.rank1++
	}
	if rank > 0 && rank <= evalTop5 {
		t.r5++
	}
	if rank > 0 {
		t.found++
		t.mrr += 1 / float64(rank)
	}
}

func (t *evalTally) metric(name string) float64 {
	if t.n == 0 {
		return 0
	}
	switch name {
	case "rank1":
		return float64(t.rank1) / float64(t.n)
	case "r5":
		return float64(t.r5) / float64(t.n)
	case "found":
		return float64(t.found) / float64(t.n)
	case "mrr":
		return t.mrr / float64(t.n)
	}
	return 0
}

// evalFloor is a coarse regression floor. The floors sit well below the measured
// baseline on purpose: they must survive a slightly different corpus (a few more
// turns, a renamed session) but catch a real ranking regression — a lost plural
// fallback, a bonus dropped from the score, a broken union rule.
//
// The baselines quoted in `why` are what THIS harness measures on the local corpus
// (the scoreboard above every run prints them). They are lower than the plan's probe
// numbers because the queries here are built by a different, deterministic rule.
type evalFloor struct {
	set    string // a set name, or "mixed" for the pooled total
	metric string // rank1 | r5 | found | mrr
	min    float64
	why    string
}

var evalFloors = []evalFloor{
	{"label", "rank1", 0.40, "baseline 56.8% — labels are ambiguous across sessions, so the floor stays loose"},
	{"label", "r5", 0.70, "baseline 86.4%"},
	{"body", "rank1", 0.65, "baseline 79.3%"},
	{"body", "r5", 0.90, "baseline 100.0%"},
	{"title", "rank1", 0.65, "baseline 79.0% — the title bonus is what makes this set score"},
	{"title", "r5", 0.85, "baseline 96.0%"},
	{"plural", "rank1", 0.60, "baseline 73.3%; naive per-term MATCH scores 6.7%, so this pins the singular fallback"},
	{"plural", "r5", 0.90, "baseline 100.0%"},
	{"cross", "rank1", 0.75, "baseline 95.0%"},
	{"cross", "found", 0.75, "baseline 60/60 (45/60 is the plan's floor); a per-turn AND finds 9/60, so this pins the union rule"},
	{"mixed", "rank1", 0.60, "baseline 71.1%"},
	{"mixed", "r5", 0.85, "baseline 93.9%"},
	{"mixed", "mrr", 0.70, "baseline 0.811"},
}

func TestSearchQuality(t *testing.T) {
	dir := strings.TrimSpace(os.Getenv(evalEnvDir))
	if dir == "" {
		t.Skipf("ranking eval skipped: set %s to a memory corpus directory (e.g. $PWD/.ogcode/memory)", evalEnvDir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", evalEnvDir, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		t.Fatalf("%s=%s is not a directory", evalEnvDir, abs)
	}

	corpus, err := loadEvalCorpus(abs)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(corpus.turns) < 50 || len(corpus.sessions) < 5 {
		t.Skipf("corpus too small to score: %d turn files, %d sessions", len(corpus.turns), len(corpus.sessions))
	}
	t.Logf("corpus: %d turn files, %d sessions, project %q%s", len(corpus.turns), len(corpus.sessions), corpus.projectID, evalSkippedNote(corpus.skipped))

	database, err := db.Open(filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatalf("open eval db: %v", err)
	}
	defer database.Close()

	// The search store drops hits whose session has no row (ghost conversations),
	// and an eval corpus carries no transcripts — so seed one row per session
	// before indexing, or every query returns nothing.
	if err := seedEvalSessions(database, corpus); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}

	sets := buildEvalSets(corpus)
	t.Logf("sets: %s fingerprint=%016x", evalSetSummary(sets), evalSetFingerprint(sets))

	store := NewSearchStore(database)
	projectDir, stats := evalIndex(t, store, abs, corpus)
	t.Logf("index: projectDir=%s stats=%+v", projectDir, stats)

	ctx := context.Background()
	missLimit := evalMissLimit()
	misses := make(map[string]int, len(evalSetOrder))
	tallies := make(map[string]*evalTally, len(evalSetOrder))
	mixed := &evalTally{}
	for _, name := range evalSetOrder {
		tal := &evalTally{}
		for _, c := range sets[name] {
			hits, err := store.Search(ctx, corpus.projectID, projectDir, c.query, evalSearchLimit)
			if err != nil {
				t.Fatalf("search %q: %v", c.query, err)
			}
			rank := evalTargetRank(hits, c.sessionID)
			tal.add(rank)
			mixed.add(rank)
			evalLogMiss(t, missLimit, misses, name, c, rank, hits, corpus)
		}
		tallies[name] = tal
	}

	evalLogScoreboard(t, sets, tallies, mixed)
	evalCheckFloors(t, tallies, mixed)
}

// evalSetSummary renders the query count of every set on one line.
func evalSetSummary(sets map[string][]evalCase) string {
	parts := make([]string, 0, len(evalSetOrder))
	for _, name := range evalSetOrder {
		parts = append(parts, fmt.Sprintf("%s=%d", name, len(sets[name])))
	}
	return strings.Join(parts, " ")
}

// evalSetFingerprint hashes the graded queries themselves. Two runs on one corpus
// must print the same value: that is what "the sets are deterministic" means
// here, and it makes a score change attributable to the ranking rather than to a
// different sample.
func evalSetFingerprint(sets map[string][]evalCase) uint64 {
	h := fnv.New64a()
	for _, name := range evalSetOrder {
		for _, c := range sets[name] {
			fmt.Fprintf(h, "%s\x00%s\x00%s\x00", name, c.query, c.sessionID)
		}
	}
	return h.Sum64()
}

// evalSkippedNote renders the "N files skipped" suffix, or nothing.
func evalSkippedNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d files skipped (no session_id)", n)
}

// loadEvalCorpus reads every turn summary directly under dir (the sessions/
// subfolder is session memory, not turn files, and is not indexed by this test).
func loadEvalCorpus(dir string) (*evalCorpus, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	c := &evalCorpus{sessions: make(map[string][]*evalTurn)}
	projectVotes := make(map[string]int)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		turn := parseEvalTurn(e.Name(), string(raw))
		if turn.sessionID == "" {
			c.skipped++
			continue
		}
		if turn.projectID != "" {
			projectVotes[turn.projectID]++
		}
		c.turns = append(c.turns, turn)
	}
	sort.Slice(c.turns, func(i, j int) bool { return c.turns[i].rel < c.turns[j].rel })
	for _, turn := range c.turns {
		c.sessions[turn.sessionID] = append(c.sessions[turn.sessionID], turn)
	}
	for id := range c.sessions {
		c.sessionIDs = append(c.sessionIDs, id)
	}
	sort.Strings(c.sessionIDs)
	c.projectID = evalModal(projectVotes)
	return c, nil
}

// evalModal returns the most-voted value, breaking ties lexically so the choice
// is stable across runs.
func evalModal(votes map[string]int) string {
	best, bestN := "", -1
	for k, n := range votes {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// parseEvalTurn pulls the fields the indexer itself derives: session attribution
// and title from the frontmatter (H1 as the title fallback), and labels through
// the indexer's own extractTopics so the eval and the index cannot disagree.
func parseEvalTurn(name, raw string) *evalTurn {
	fm := evalFrontmatter(raw)
	turn := &evalTurn{
		rel:       name,
		sessionID: strings.TrimSpace(fm["session_id"]),
		projectID: strings.TrimSpace(fm["project_id"]),
		title:     strings.TrimSpace(fm["title"]),
		labels:    extractTopics(raw),
		terms:     evalTermSet(raw),
	}
	if turn.title == "" {
		turn.title = evalH1(raw)
	}
	return turn
}

func evalFrontmatter(raw string) map[string]string {
	fm := make(map[string]string)
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			return fm
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		fm[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return fm
}

func evalH1(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(trimmed[2:])
		}
	}
	return ""
}

// evalTerms lowercases and splits on everything that is not a letter or digit,
// dropping one-character tokens and stopwords — the ranking tokenizer's shape.
func evalTerms(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len([]rune(f)) < 2 {
			continue
		}
		if _, stop := evalStopwords[f]; stop {
			continue
		}
		out = append(out, f)
	}
	return out
}

func evalTermSet(s string) map[string]struct{} {
	terms := evalTerms(s)
	set := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		set[t] = struct{}{}
	}
	return set
}

// buildEvalSets builds every query set from the corpus itself. Each one is
// deterministic: candidates are gathered by walking the corpus in path order,
// words within a turn are ordered by (document frequency, word), and the final
// selection is an evenly spaced slice of a sorted candidate list — no random
// sampling anywhere, so two runs on the same corpus grade the same queries.
func buildEvalSets(c *evalCorpus) map[string][]evalCase {
	df := c.docFreq()
	body := buildEvalBodySet(c, df, evalBodyQueries)
	plural := buildEvalPluralSet(c, df, body, evalPluralQueries)
	sets := map[string][]evalCase{
		"label":  buildEvalLabelSet(c, evalLabelQueries),
		"body":   body,
		"title":  buildEvalTitleSet(c, evalTitleQueries),
		"plural": plural,
		"cross":  buildEvalCrossSet(c, df, evalCrossTurnQueries),
	}
	return sets
}

// docFreq counts, per term, how many turn files contain it.
func (c *evalCorpus) docFreq() map[string]int {
	df := make(map[string]int)
	for _, turn := range c.turns {
		for term := range turn.terms {
			df[term]++
		}
	}
	return df
}

// buildEvalLabelSet asks for a session by one of its own `Topics:` labels.
func buildEvalLabelSet(c *evalCorpus, n int) []evalCase {
	seen := make(map[string]struct{})
	var cands []evalCase
	for _, turn := range c.turns {
		for _, label := range turn.labels {
			query := strings.TrimSpace(label)
			if len([]rune(query)) < 2 {
				continue
			}
			key := strings.ToLower(query)
			if _, dup := seen[key]; dup {
				continue // one query per label: the first session that claimed it
			}
			seen[key] = struct{}{}
			cands = append(cands, evalCase{query: query, sessionID: turn.sessionID})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].query < cands[j].query })
	return evalSample(cands, n)
}

// buildEvalBodySet asks for a session by two rare words that sit inside one turn.
// The words are drawn from the middle of each turn's rare-word band once it is
// sorted by document frequency, so the set is not dominated by words that occur
// in exactly one turn of the whole corpus (trivially findable, and no signal
// about ranking).
func buildEvalBodySet(c *evalCorpus, df map[string]int, n int) []evalCase {
	seen := make(map[string]struct{})
	var cands []evalCase
	for _, turn := range c.turns {
		rare := evalRareWords(turn, df)
		if len(rare) < 2 {
			continue
		}
		w1, w2 := rare[len(rare)/3], rare[2*len(rare)/3]
		if w1 == w2 {
			continue
		}
		query := w1 + " " + w2
		if _, dup := seen[query]; dup {
			continue
		}
		seen[query] = struct{}{}
		cands = append(cands, evalCase{query: query, sessionID: turn.sessionID})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].query < cands[j].query })
	return evalSample(cands, n)
}

// buildEvalPluralSet takes the body queries and pluralises one of their words —
// the defect that made plurals the largest single ranking problem (6.7% naive vs
// 75% with the singular fallback). A pair whose plural already exists verbatim in
// the corpus is skipped: that query would not force the fallback at all.
func buildEvalPluralSet(c *evalCorpus, df map[string]int, body []evalCase, n int) []evalCase {
	var cands []evalCase
	for _, bc := range body {
		words := strings.Fields(bc.query)
		if len(words) != 2 {
			continue
		}
		var query string
		for _, i := range []int{0, 1} {
			plural := evalPlural(words[i])
			if plural == "" {
				continue
			}
			if _, exists := df[plural]; exists {
				continue
			}
			other := words[1-i]
			query = plural + " " + other
			break
		}
		if query == "" {
			continue
		}
		cands = append(cands, evalCase{query: query, sessionID: bc.sessionID})
	}
	// cands already follows the body set's order, which is sorted and stable.
	return evalSample(cands, n)
}

// buildEvalCrossSet asks for a session by two rare words taken from two DIFFERENT
// turns of it — the queries a per-turn MATCH can never answer, which is exactly
// what the union-candidate rule buys (57/60 vs 9/60). Pairs whose words also
// co-occur in some single turn of the same session are rejected for the same
// reason.
func buildEvalCrossSet(c *evalCorpus, df map[string]int, n int) []evalCase {
	var cands []evalCase
	for _, id := range c.sessionIDs {
		turns := c.sessions[id]
		if len(turns) < 2 {
			continue
		}
		if query, ok := evalCrossPair(turns, df); ok {
			cands = append(cands, evalCase{query: query, sessionID: id})
		}
	}
	return evalSample(cands, n)
}

// evalCrossPair finds a deterministic two-word query for one multi-turn session,
// or reports that it has none.
func evalCrossPair(turns []*evalTurn, df map[string]int) (string, bool) {
	const spread = 4 // turn pairs considered, in path order
	const depth = 6  // rare-word candidates per turn, rarest first
	limit := len(turns)
	if limit > spread {
		limit = spread
	}
	for i := 0; i < limit; i++ {
		wi := evalRareWords(turns[i], df)
		if len(wi) > depth {
			wi = wi[:depth]
		}
		for j := i + 1; j < limit; j++ {
			wj := evalRareWords(turns[j], df)
			if len(wj) > depth {
				wj = wj[:depth]
			}
			for _, a := range wi {
				for _, b := range wj {
					if a == b || evalCoOccur(turns, a, b) {
						continue
					}
					return a + " " + b, true
				}
			}
		}
	}
	return "", false
}

// evalCoOccur reports whether any single turn of the session holds both words —
// such a query is answerable by a per-turn AND and would not exercise the union
// rule.
func evalCoOccur(turns []*evalTurn, a, b string) bool {
	for _, turn := range turns {
		_, hasA := turn.terms[a]
		_, hasB := turn.terms[b]
		if hasA && hasB {
			return true
		}
	}
	return false
}

// buildEvalTitleSet asks for a session by two words of its title — "I remember
// what I called it". One query per session (its earliest turn's title), the two
// RAREST title words, so the query names the session rather than "the build".
// A pair that also titles another session is dropped: two sessions share those
// words, so no ranking can put the right one first and scoring it measures luck.
func buildEvalTitleSet(c *evalCorpus, n int) []evalCase {
	titleDF := make(map[string]int)
	for _, turn := range c.turns {
		for term := range evalTermSet(turn.title) {
			titleDF[term]++
		}
	}
	type pick struct {
		sessionID, w1, w2, key string
	}
	var picks []pick
	pairs := make(map[string]int)
	for _, id := range c.sessionIDs {
		words := evalTitleWords(c.sessions[id][0].title, titleDF)
		if len(words) < 2 {
			continue
		}
		w1, w2 := words[0], words[1]
		key := w1 + " " + w2
		if w2 < w1 {
			key = w2 + " " + w1
		}
		picks = append(picks, pick{sessionID: id, w1: w1, w2: w2, key: key})
		pairs[key]++
	}
	seen := make(map[string]struct{})
	var cands []evalCase
	for _, p := range picks {
		if pairs[p.key] > 1 {
			continue // ambiguous by construction, see above
		}
		query := p.w1 + " " + p.w2
		if _, dup := seen[query]; dup {
			continue
		}
		seen[query] = struct{}{}
		cands = append(cands, evalCase{query: query, sessionID: p.sessionID})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].query < cands[j].query })
	return evalSample(cands, n)
}

// evalTitleWords returns a title's meaningful words, rarest across all titles
// first, ties broken lexically — the order the title set selects from.
func evalTitleWords(title string, titleDF map[string]int) []string {
	var words []string
	for _, word := range evalTerms(title) {
		if evalPlainWord(word, 3) {
			words = append(words, word)
		}
	}
	sort.Slice(words, func(i, j int) bool {
		if titleDF[words[i]] != titleDF[words[j]] {
			return titleDF[words[i]] < titleDF[words[j]]
		}
		return words[i] < words[j]
	})
	return words
}

// evalPlainWord reports whether a term is a word a person would type: letters
// only, at least min runes. Turn bodies carry ULIDs, hex ids and file hashes;
// the tokenizer keeps them, they sit in the rare band, and as half of a two-word
// query they match nothing a person would ask for.
func evalPlainWord(term string, min int) bool {
	if len(term) < min {
		return false
	}
	for _, r := range term {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// evalRareWords returns a turn's terms inside the rare band, rarest first, ties
// broken lexically — the stable order every set builder selects from. Junk
// tokens (ULIDs, hex ids, hashes) are excluded: see evalPlainWord.
func evalRareWords(turn *evalTurn, df map[string]int) []string {
	var rare []string
	for term := range turn.terms {
		if !evalPlainWord(term, 4) {
			continue
		}
		if d := df[term]; d >= evalRareDFMin && d <= evalRareDFMax {
			rare = append(rare, term)
		}
	}
	sort.Slice(rare, func(i, j int) bool {
		if df[rare[i]] != df[rare[j]] {
			return df[rare[i]] < df[rare[j]]
		}
		return rare[i] < rare[j]
	})
	return rare
}

// evalPlural pluralises a word the way a user types it. Only the regular English
// endings matter here — the corpus is English prose. A word already ending in s
// is already a plural: pluralising it again builds "catcheses", which nobody
// types and no singular fallback can recover, so such a word reports "".
func evalPlural(word string) string {
	if strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return ""
	}
	if strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])) {
		return word[:len(word)-1] + "ies"
	}
	for _, suffix := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(word, suffix) {
			return word + "es"
		}
	}
	return word + "s"
}

// evalSample takes n evenly spaced entries from a sorted candidate list, so the
// selection covers the whole corpus instead of its first alphabetical slice.
func evalSample(cands []evalCase, n int) []evalCase {
	if len(cands) <= n {
		return cands
	}
	out := make([]evalCase, 0, n)
	for i := 0; i < n; i++ {
		idx := int(float64(i) * float64(len(cands)-1) / float64(n-1))
		out = append(out, cands[idx])
	}
	return out
}

// evalIndex builds the search index over the eval corpus and returns the project
// directory Search should be called with. A corpus is normally a project's own
// .ogcode/memory folder; any other path is mirrored into a throwaway project
// layout by symlink, so the harness works on a copied corpus too. The probe after
// indexing catches a store that indexed nothing, rather than silently scoring
// zero.
func evalIndex(t *testing.T, store *SearchStore, corpusDir string, corpus *evalCorpus) (string, ReindexStats) {
	t.Helper()
	probe := evalProbeQuery(corpus)
	tryDir := func(projectDir string) (ReindexStats, bool) {
		stats, err := store.ReindexProject(projectDir, corpus.projectID)
		if err != nil {
			t.Logf("reindex %s: %v", projectDir, err)
			return stats, false
		}
		return stats, evalProbeHits(store, corpus, projectDir, probe) > 0
	}

	if filepath.Base(filepath.Dir(corpusDir)) == ".ogcode" {
		if stats, ok := tryDir(filepath.Dir(filepath.Dir(corpusDir))); ok {
			return filepath.Dir(filepath.Dir(corpusDir)), stats
		}
	}

	projectDir := filepath.Join(t.TempDir(), "evalproj")
	memoryDir := filepath.Join(projectDir, ".ogcode", "memory")
	if err := os.MkdirAll(filepath.Dir(memoryDir), 0o755); err != nil {
		t.Fatalf("create eval project layout: %v", err)
	}
	if err := os.Symlink(corpusDir, memoryDir); err != nil {
		t.Fatalf("link corpus into eval project: %v", err)
	}
	stats, ok := tryDir(projectDir)
	if !ok {
		t.Fatalf("ReindexProject indexed nothing from %s (stats=%+v) — the corpus was not made searchable", corpusDir, stats)
	}
	return projectDir, stats
}

// evalProbeQuery picks a query that must return at least one hit when the corpus
// is indexed: the first turn's first usable label, or failing that a rare word.
func evalProbeQuery(corpus *evalCorpus) string {
	for _, turn := range corpus.turns {
		for _, label := range turn.labels {
			if q := strings.TrimSpace(label); len([]rune(q)) >= 2 {
				return q
			}
		}
	}
	for _, turn := range corpus.turns {
		for term := range turn.terms {
			return term
		}
	}
	return ""
}

func evalProbeHits(store *SearchStore, corpus *evalCorpus, projectDir, query string) int {
	if query == "" {
		return 0
	}
	hits, err := store.Search(context.Background(), corpus.projectID, projectDir, query, 1)
	if err != nil {
		return 0
	}
	return len(hits)
}

// titleOf is one session's title, for the miss log.
func (c *evalCorpus) titleOf(sessionID string) string {
	if turns := c.sessions[sessionID]; len(turns) > 0 {
		return turns[0].title
	}
	return sessionID
}

// seedEvalSessions writes one session row per corpus session: search results for
// ghost conversations are dropped, and a memory corpus has no transcripts.
func seedEvalSessions(database *db.DB, corpus *evalCorpus) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO session
		(id, project_id, directory, title, permission, time_created, time_updated)
		VALUES (?, ?, ?, ?, NULL, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range corpus.sessionIDs {
		turns := corpus.sessions[id]
		title := ""
		if len(turns) > 0 {
			title = turns[0].title
		}
		if _, err := stmt.Exec(id, corpus.projectID, "", title, 0, 0); err != nil {
			return fmt.Errorf("insert session %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// evalTargetRank is the 1-based position of the expected session in the hits, or
// 0 when it is not in the returned window.
func evalTargetRank(hits []Hit, sessionID string) int {
	for i, hit := range hits {
		if hit.SessionID == sessionID {
			return i + 1
		}
	}
	return 0
}

// evalMissLimit reads the debug depth: 0 (the default) prints no misses at all.
func evalMissLimit() int {
	raw := strings.TrimSpace(os.Getenv(evalEnvDebug))
	if raw == "" || raw == "0" {
		return 0
	}
	if raw == "all" {
		return -1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// evalLogMiss prints a query the ranking got wrong together with the sessions it
// ranked above the expected one, so a floor breach reads as a concrete defect
// instead of a bare percentage.
func evalLogMiss(t *testing.T, limit int, shown map[string]int, set string, c evalCase, rank int, hits []Hit, corpus *evalCorpus) {
	t.Helper()
	if limit == 0 || rank == 1 {
		return
	}
	if limit > 0 && shown[set] >= limit {
		return
	}
	shown[set]++
	var top []string
	for i, hit := range hits {
		if i >= 3 {
			break
		}
		top = append(top, fmt.Sprintf("%d. %s (%.3f, %s)", i+1, hit.Title, hit.Score, hit.Source))
	}
	if len(top) == 0 {
		top = []string{"(no hits)"}
	}
	where := "rank " + strconv.Itoa(rank)
	if rank == 0 {
		where = "not found"
	}
	t.Logf("miss: set=%-6s q=%q want=%q got=%s hits=[%s]",
		set, c.query, corpus.titleOf(c.sessionID), where, strings.Join(top, "; "))
}

// evalLogScoreboard prints the per-set scoreboard in the plan's Accuracy shape so
// a diff across runs is readable at a glance.
func evalLogScoreboard(t *testing.T, sets map[string][]evalCase, tallies map[string]*evalTally, mixed *evalTally) {
	t.Helper()
	t.Logf("%-8s %5s %9s %8s %8s %8s", "set", "n", "rank-1", "r@5", "found", "MRR")
	for _, name := range evalSetOrder {
		tal := tallies[name]
		label := name
		if got := len(sets[name]); got != tal.n {
			label = fmt.Sprintf("%s*", name)
		}
		t.Logf("%-8s %5d %8.1f%% %7.1f%% %7.1f%% %8.3f",
			label, tal.n,
			100*tal.metric("rank1"), 100*tal.metric("r5"), 100*tal.metric("found"), tal.metric("mrr"))
	}
	t.Logf("%-8s %5d %8.1f%% %7.1f%% %7.1f%% %8.3f",
		"mixed", mixed.n,
		100*mixed.metric("rank1"), 100*mixed.metric("r5"), 100*mixed.metric("found"), mixed.metric("mrr"))
	for _, name := range evalSetOrder {
		if len(sets[name]) < evalMinSetSize(name) {
			t.Logf("note: %s set is short (%d of %d queries) — the corpus did not yield enough candidates", name, len(sets[name]), evalMinSetSize(name))
		}
	}
}

// evalMinSetSize is the query count each set aims for.
func evalMinSetSize(name string) int {
	switch name {
	case "label":
		return evalLabelQueries
	case "body":
		return evalBodyQueries
	case "title":
		return evalTitleQueries
	case "plural":
		return evalPluralQueries
	case "cross":
		return evalCrossTurnQueries
	}
	return 1
}

// evalCheckFloors is the gate itself: a coarse per-set floor, far enough below
// the measured baseline not to flake, close enough to catch a real regression.
func evalCheckFloors(t *testing.T, tallies map[string]*evalTally, mixed *evalTally) {
	t.Helper()
	for _, f := range evalFloors {
		tal := mixed
		if f.set != "mixed" {
			tal = tallies[f.set]
		}
		if tal == nil || tal.n == 0 {
			continue // a set the corpus could not build is reported, not failed
		}
		got := tal.metric(f.metric)
		if got < f.min {
			t.Errorf("ranking regression: %s %s = %s, below the floor %s (%s)",
				f.set, f.metric, evalFormatMetric(f.metric, got), evalFormatMetric(f.metric, f.min), f.why)
			continue
		}
		t.Logf("floor ok: %-6s %-5s %s >= %s", f.set, f.metric, evalFormatMetric(f.metric, got), evalFormatMetric(f.metric, f.min))
	}
}

func evalFormatMetric(metric string, v float64) string {
	if metric == "mrr" {
		return fmt.Sprintf("%.3f", v)
	}
	return fmt.Sprintf("%.1f%%", 100*v)
}
