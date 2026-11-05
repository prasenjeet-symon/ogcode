"""Score a document against a query, synonyms included.

The score is the share of the query's terms the document carries, after
both sides are folded and expanded through the synonym groups. It answers
the question the storefront asks first (``should this product appear?'') and
leaves BM25 ordering to :mod:`shop.search.ranker`.
"""

from shop.search.normalizer import normalize
from shop.search.synonyms import expand

__all__ = ["expand_terms", "missing", "score"]


def expand_terms(terms) -> set:
    """Every spelling accepted for the given terms."""
    wanted = set()
    for term in terms:
        wanted.update(expand(term))
    return wanted


def score(query: str, text: str) -> float:
    """Share of the query's terms the document carries, 0.0 to 1.0."""
    wanted = expand_terms(normalize(query))
    if not wanted:
        return 0.0
    present = set(normalize(text))
    if not present:
        return 0.0
    return round(len(wanted & present) / len(wanted), 4)


def missing(query: str, text: str) -> list:
    """The query's terms the document does not carry, sorted."""
    wanted = expand_terms(normalize(query))
    present = set(normalize(text))
    return sorted(wanted - present)
