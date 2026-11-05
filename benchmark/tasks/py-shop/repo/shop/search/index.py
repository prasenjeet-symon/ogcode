"""An in-memory inverted index over product text.

Documents are added by id, so re-indexing a product replaces its postings
instead of doubling them. Term frequencies are kept per document because
the BM25 ranker needs them; nothing here touches the disk.
"""

from shop.search.normalizer import normalize

__all__ = ["InvertedIndex"]


class InvertedIndex:
    """Term to ``{document id: term frequency}`` plus per-document lengths."""

    def __init__(self):
        self._postings = {}
        self._lengths = {}

    def add(self, doc_id: str, text: str) -> int:
        """Index one document, replacing any earlier version of it."""
        self.remove(doc_id)
        tokens = normalize(text)
        self._lengths[doc_id] = len(tokens)
        for token in tokens:
            bucket = self._postings.setdefault(token, {})
            bucket[doc_id] = bucket.get(doc_id, 0) + 1
        return len(tokens)

    def remove(self, doc_id: str) -> bool:
        """Drop a document; ``False`` when it was never indexed."""
        if self._lengths.pop(doc_id, None) is None:
            return False
        for term in list(self._postings):
            bucket = self._postings[term]
            bucket.pop(doc_id, None)
            if not bucket:
                del self._postings[term]
        return True

    def postings(self, term: str) -> dict:
        """A copy of the term's document frequencies, never the live dict."""
        return dict(self._postings.get(term, {}))

    def doc_length(self, doc_id: str) -> int:
        """How many tokens the document carried when it was indexed."""
        return int(self._lengths.get(doc_id, 0))

    def doc_freq(self, term: str) -> int:
        """How many documents carry the term."""
        return len(self._postings.get(term, {}))

    @property
    def doc_count(self) -> int:
        """How many documents the index holds."""
        return len(self._lengths)

    def avg_length(self) -> float:
        """Mean document length in tokens, zero for an empty index."""
        if not self._lengths:
            return 0.0
        return sum(self._lengths.values()) / len(self._lengths)

    def terms(self) -> list:
        """Every indexed term, sorted."""
        return sorted(self._postings)

    def doc_ids(self) -> list:
        """Every indexed document id, sorted."""
        return sorted(self._lengths)
