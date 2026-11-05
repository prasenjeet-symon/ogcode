"""Parse a shopper query into terms, phrases, exclusions and filters.

The grammar is the one the storefront search box accepts::

    red shoes -blue brand:acme "wide fit"

Words are free terms, a quoted run is a phrase that must appear whole, a
leading ``-`` excludes a term and ``field:value`` becomes a facet filter.
Parsing never raises on user input: an unbalanced quote is closed for you
and an empty query parses to an empty search.
"""

import shlex

from shop.search.normalizer import normalize

__all__ = ["FIELDS", "parse"]

FIELDS = ("brand", "category", "color", "size", "tier", "channel")


def parse(text: str) -> dict:
    """The structured form of a query string."""
    parsed = {"terms": [], "phrases": [], "negated": [], "filters": {}}
    raw = str(text or "")
    try:
        words = shlex.split(raw)
    except ValueError:
        words = raw.replace('"', " ").split()
    for word in words:
        if ":" in word and not word.startswith("-"):
            field, _, value = word.partition(":")
            if field.lower() in FIELDS and value.strip():
                parsed["filters"][field.lower()] = value.strip().lower()
                continue
        if word.startswith("-") and len(word) > 1:
            parsed["negated"].extend(normalize(word[1:]))
        elif len(word.split()) > 1:
            phrase = " ".join(normalize(word))
            if phrase:
                parsed["phrases"].append(phrase)
        else:
            parsed["terms"].extend(normalize(word))
    parsed["terms"] = [term for term in parsed["terms"] if term]
    return parsed
