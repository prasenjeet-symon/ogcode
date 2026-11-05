"""BM25 ordering over the inverted index.

BM25 rewards a document that repeats a query term and penalises one that is
long enough to mention everything, which is exactly the behaviour the
catalogue needs: a short, specific product page outranks a category blurb
that happens to contain the same word.
"""

import math

from shop.search.normalizer import normalize

__all__ = ["B", "K1", "bm25"]

K1 = 1.5
B = 0.75


def bm25(index, query: str, limit: int = 10) -> list:
    """``(doc_id, score)`` pairs, best first, ties broken by document id."""
    terms = normalize(query)
    if not terms or not index.doc_count:
        return []
    total = index.doc_count
    average = index.avg_length() or 1.0
    scores = {}
    for term in terms:
        postings = index.postings(term)
        if not postings:
            continue
        idf = math.log(1.0 + (total - len(postings) + 0.5) / (len(postings) + 0.5))
        for doc_id, frequency in postings.items():
            length = index.doc_length(doc_id) or 1
            norm = K1 * (1 - B + B * length / average)
            weight = idf * frequency * (K1 + 1) / (frequency + norm)
            scores[doc_id] = scores.get(doc_id, 0.0) + weight
    ranked = sorted(scores.items(), key=lambda row: (-row[1], str(row[0])))
    return [(doc_id, round(score, 6)) for doc_id, score in ranked[: max(0, int(limit))]]
