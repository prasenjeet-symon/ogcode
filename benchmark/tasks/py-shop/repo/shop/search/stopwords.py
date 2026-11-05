"""Words too common to carry meaning in a product search.

Stop words are dropped when a document is indexed and again when a query is
parsed, so ``the red shoes`` and ``red shoes`` are the same search. The set
is intentionally small: dropping an adjective that shoppers actually use
loses more recall than keeping a filler word costs precision.
"""

__all__ = ["STOPWORDS", "content_words", "is_stopword"]

STOPWORDS = frozenset(
    (
        "a", "about", "after", "all", "also", "an", "and", "any", "are",
        "as", "at", "be", "been", "but", "by", "can", "do", "does", "for",
        "from", "had", "has", "have", "how", "if", "in", "into", "is", "it",
        "its", "just", "more", "most", "my", "no", "not", "of", "on", "or",
        "our", "out", "over", "same", "she", "so", "some", "than", "that",
        "the", "their", "them", "then", "there", "these", "they", "this",
        "those", "to", "too", "up", "us", "very", "was", "we", "were",
        "what", "when", "where", "which", "who", "why", "will", "with", "you",
        "your",
    )
)


def is_stopword(token: str) -> bool:
    """True for a word the search engine ignores."""
    return token in STOPWORDS


def content_words(tokens) -> list:
    """The tokens that survive the stop list, order preserved."""
    return [token for token in tokens if token not in STOPWORDS]
