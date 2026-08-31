package memory

import (
	"math"
	"sort"
)

// Relevance selection for semantic recall.
//
// Normalized sentence embedders do not spread cosine similarity across 0→1.
// gte-small, the bundled model, scores two entirely *unrelated* English
// passages at roughly 0.72–0.85 — measured across this store's own corpus, the
// median similarity between facts drawn from different sessions is 0.78. An
// absolute gate of 0.1 therefore admits ~99% of unrelated text: it is not a
// filter at all, and recall was handing the synthesis LLM every fact in the
// session, then paying for it three times over in refinement rounds.
//
// Selection is relative instead. A fact has to stand out from the score
// distribution of the facts it competes with, which self-calibrates per query
// and survives an embedder swap — something a hand-tuned absolute cutoff
// cannot do, because the cutoff is a property of the model, not of relevance.
const (
	// minUsableCosine drops vectors that are broken rather than merely
	// irrelevant: a zero-length or corrupt embedding scores near 0 against
	// everything. It is a sanity floor, NOT the relevance filter — see
	// selectByRelevance for that.
	minUsableCosine = 0.1

	// relevanceZCut keeps facts scoring at least this many standard deviations
	// above the candidate pool's mean cosine. 1.0 keeps roughly the top sixth
	// of a normally distributed pool.
	relevanceZCut = 1.0

	// minPoolForZCut is the pool size below which the adaptive gate is skipped.
	// A mean and standard deviation over a handful of facts describe noise, and
	// the top-K cap already bounds the result on its own.
	minPoolForZCut = 5

	// recencyWeight is the recency tiebreaker, applied to scores AFTER they are
	// normalized into [0,1] across the surviving pool. Applied to raw cosine it
	// was actively harmful: a 0.15 boost exceeded the entire observed spread
	// between a session's best and worst match (~0.11), so ranking was recency
	// order with semantic similarity as noise on top.
	recencyWeight = 0.15
)

// scoreStats accumulates the mean and standard deviation of a stream of scores
// in one pass, so the adaptive gate can be computed without buffering every
// score or walking the corpus twice. scanProject in particular sees facts
// through a streaming callback and never holds them all at once.
type scoreStats struct {
	n     int
	sum   float64
	sumSq float64
}

func (s *scoreStats) add(v float32) {
	f := float64(v)
	s.n++
	s.sum += f
	s.sumSq += f * f
}

func (s scoreStats) mean() float64 {
	if s.n == 0 {
		return 0
	}
	return s.sum / float64(s.n)
}

func (s scoreStats) stddev() float64 {
	if s.n < 2 {
		return 0
	}
	// Population variance. Accumulated sums lose precision against a mean near
	// 0.8, and the subtraction can land just below zero for a pool whose scores
	// are all but identical; clamp rather than hand math.Sqrt a negative.
	variance := s.sumSq/float64(s.n) - s.mean()*s.mean()
	if variance <= 0 {
		return 0
	}
	return math.Sqrt(variance)
}

// cut returns the minimum cosine a fact must reach to clear the adaptive gate.
// ok is false when the pool is too small, or too uniform, for the distribution
// to say anything — the caller should then rank without gating.
func (s scoreStats) cut(z float64) (float32, bool) {
	if s.n < minPoolForZCut {
		return 0, false
	}
	sd := s.stddev()
	if sd <= 0 {
		return 0, false
	}
	return float32(s.mean() + z*sd), true
}

// selectByRelevance gates candidates against the score distribution they were
// drawn from, re-ranks the survivors with a recency tiebreaker, and caps the
// result at limit.
//
// candidates carry RAW cosine in .score; the returned facts carry the blended
// ranking score instead, which is comparable only within one call.
//
// stats must describe every fact that was scored, not just the candidates that
// reached this call — a caller that pre-trims (project recall caps per session
// before the global cut) would otherwise gate against an already-selected pool
// and cut a second time from the same tail.
//
// recency maps a node to [0,1], newest = 1. Nil disables the tiebreaker.
func selectByRelevance(candidates []scoredFact, stats scoreStats, limit int, recency func(Node) float32) []scoredFact {
	if len(candidates) == 0 || limit <= 0 {
		return nil
	}

	kept := candidates
	if cut, ok := stats.cut(relevanceZCut); ok {
		gated := make([]scoredFact, 0, len(candidates))
		for _, c := range candidates {
			if c.score >= cut {
				gated = append(gated, c)
			}
		}
		// An empty gate means nothing stood out from the pool — which is a
		// statement about the spread, not evidence that memory holds nothing
		// relevant. The best few matches are still the best answer available,
		// so fall through to ranking rather than reporting an empty recall.
		if len(gated) > 0 {
			kept = gated
		}
	}

	minScore, maxScore := kept[0].score, kept[0].score
	for _, c := range kept {
		if c.score < minScore {
			minScore = c.score
		}
		if c.score > maxScore {
			maxScore = c.score
		}
	}
	spread := maxScore - minScore

	ranked := make([]scoredFact, len(kept))
	for i, c := range kept {
		// Normalizing into [0,1] is what makes recencyWeight mean "a fraction
		// of the observed spread" rather than an absolute number that has to be
		// guessed against whatever floor the current embedder happens to have.
		norm := float32(1)
		if spread > 0 {
			norm = (c.score - minScore) / spread
		}
		var r float32
		if recency != nil {
			r = recency(c.node)
		}
		ranked[i] = scoredFact{node: c.node, score: norm + recencyWeight*r}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		// Ties would otherwise resolve differently on each call, and two
		// identical recalls must build the same prompt. Project recall makes
		// this reachable twice over: it merges facts from many sessions, where
		// Key is only unique within one session, and it collects them by
		// ranging a map, so the input order is already arbitrary.
		if ranked[i].node.SessionID != ranked[j].node.SessionID {
			return ranked[i].node.SessionID < ranked[j].node.SessionID
		}
		return ranked[i].node.Key < ranked[j].node.Key
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}
