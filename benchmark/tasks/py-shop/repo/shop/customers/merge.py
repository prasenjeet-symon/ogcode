"""Merge a duplicate customer record into its primary.

The primary record always wins a field it already has. The duplicate only
fills blanks, and the counters that describe history are added together,
because a customer who bought five times under each of two records bought
ten times in total.
"""

from shop.errors import ValidationError

__all__ = ["MERGE_KEYS", "merge"]

MERGE_KEYS = ("email", "name", "phone", "tier", "accepts_marketing")
SUMMED_KEYS = ("orders_count", "lifetime_cents", "points")


def _blank(value) -> bool:
    return value is None or (isinstance(value, str) and not value.strip())


def merge(primary: dict, duplicate: dict) -> dict:
    """Return the merged record; the inputs are left untouched."""
    left = dict(primary or {})
    right = dict(duplicate or {})
    if not left.get("id") or not right.get("id"):
        raise ValidationError("both records need an id before they can merge")
    if left["id"] == right["id"]:
        raise ValidationError("cannot merge a record into itself")
    merged = dict(left)
    for key in MERGE_KEYS:
        if _blank(merged.get(key)) and not _blank(right.get(key)):
            merged[key] = right[key]
    for key in SUMMED_KEYS:
        merged[key] = int(left.get(key) or 0) + int(right.get(key) or 0)
    merged["merged_from"] = sorted(
        {str(value) for value in (left.get("merged_from") or []) + [right["id"]]}
    )
    return merged
