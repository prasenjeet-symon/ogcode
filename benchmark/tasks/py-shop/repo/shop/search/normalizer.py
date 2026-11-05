"""Fold text down to the terms the index holds.

Accents are folded (``café`` -> ``cafe``), stop words disappear and a crude
English singulariser folds plurals, so ``shoes`` and ``shoe`` hit the same
postings. The rules are deliberately conservative: a word that does not
look plural is left exactly as the shopper typed it.
"""

import unicodedata

from shop.search.stopwords import content_words
from shop.search.tokenizer import tokenize

__all__ = ["fold", "normalize", "singular"]

PLURALS = (
    ("ies", "y"),
    ("ches", "ch"),
    ("shes", "sh"),
    ("xes", "x"),
    ("zes", "z"),
    ("ses", "s"),
)


def fold(text: str) -> str:
    """Strip combining marks so accented text compares equal to plain text."""
    decomposed = unicodedata.normalize("NFKD", str(text or ""))
    return "".join(ch for ch in decomposed if not unicodedata.combining(ch))


def singular(token: str) -> str:
    """A best-effort singular form, returned unchanged when unsure."""
    word = str(token)
    if len(word) <= 3 or not word.endswith("s") or word.endswith("ss"):
        return word
    for suffix, replacement in PLURALS:
        if word.endswith(suffix):
            return word[: -len(suffix)] + replacement
    return word[:-1]


def normalize(text: str) -> list:
    """The indexable terms of ``text``, in order, duplicates kept."""
    return [singular(token) for token in content_words(tokenize(fold(text)))]
