"""Split user text into search tokens.

The indexer and the query parser both run through here, so a document and
the search that should find it agree on what a word is: lower case,
letters and digits only, apostrophes removed (``don't`` becomes ``dont``)
and hyphens split (``t-shirt`` finds ``shirt``).
"""

import re

__all__ = ["MAX_TOKEN", "TOKEN_RE", "token_set", "tokenize"]

TOKEN_RE = re.compile(r"[a-z0-9]+")

MAX_TOKEN = 48


def tokenize(text: str) -> list:
    """Lower-case tokens in the order they appear, each capped in length."""
    if not text:
        return []
    cleaned = str(text).lower().replace("'", "")
    return [token[:MAX_TOKEN] for token in TOKEN_RE.findall(cleaned)]


def token_set(text: str) -> set:
    """The distinct tokens of ``text``, for set-based scoring."""
    return set(tokenize(text))
