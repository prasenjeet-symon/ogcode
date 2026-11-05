"""Facet counting and filtering for a result page.

The search box hands back raw result dicts; the sidebar needs counts per
value and the query string needs the filters applied. Both live here so the
web layer never counts anything itself.
"""

__all__ = ["apply_filters", "facet_counts", "top_values"]


def facet_counts(values) -> dict:
    """How often each value appears, as a plain dict."""
    counts = {}
    for value in values:
        key = str(value)
        counts[key] = counts.get(key, 0) + 1
    return counts


def top_values(values, limit: int = 5) -> list:
    """``(value, count)`` pairs, most common first, ties alphabetical."""
    ranked = sorted(facet_counts(values).items(), key=lambda row: (-row[1], row[0]))
    return ranked[: max(0, int(limit))]


def apply_filters(items, filters) -> list:
    """Keep the items whose named fields hold any of the allowed values."""
    wanted = {
        str(key): {str(value) for value in values}
        for key, values in dict(filters or {}).items()
    }
    kept = []
    for item in items:
        if all(str(item.get(key)) in allowed for key, allowed in wanted.items()):
            kept.append(item)
    return kept
