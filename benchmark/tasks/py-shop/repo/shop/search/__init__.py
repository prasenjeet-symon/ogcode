"""Product search: tokenizing, an inverted index, scoring and suggestions.

The pieces are small on purpose so a caller can take one without the rest:
a facet sidebar needs only :mod:`shop.search.facets`, while the storefront
search route wires the tokenizer, index, matcher and ranker together.
"""

from shop.search.fuzzy import closest, distance
from shop.search.index import InvertedIndex
from shop.search.matcher import score
from shop.search.query import parse
from shop.search.ranker import bm25
from shop.search.tokenizer import tokenize

__all__ = [
    "InvertedIndex",
    "bm25",
    "closest",
    "distance",
    "parse",
    "score",
    "tokenize",
]
