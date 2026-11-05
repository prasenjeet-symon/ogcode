"""Type-ahead suggestions and a cheap did-you-mean.

Prefix matches are ordered shortest first, so typing ``cha`` offers ``charger``
before ``charging cable``. Did-you-mean only speaks up when a vocabulary word
is within a couple of edits; silence is better than a wrong guess.
"""

from shop.search.fuzzy import closest

__all__ = ["MAX_SUGGESTIONS", "did_you_mean", "prefix_matches"]

MAX_SUGGESTIONS = 8


def prefix_matches(prefix: str, vocabulary, limit: int = MAX_SUGGESTIONS) -> list:
    """Vocabulary entries starting with ``prefix``, shortest first."""
    stem = str(prefix or "").strip().lower()
    if not stem:
        return []
    hits = [str(word) for word in vocabulary if str(word).lower().startswith(stem)]
    hits.sort(key=lambda word: (len(word), word))
    return hits[: max(0, int(limit))]


def did_you_mean(term: str, vocabulary, max_distance: int = 2) -> str:
    """The nearest vocabulary entry, or an empty string when nothing is close."""
    hits = closest(term, vocabulary, max_distance=max_distance, limit=1)
    return hits[0] if hits else ""
