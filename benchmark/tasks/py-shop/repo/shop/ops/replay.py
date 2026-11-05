"""Replaying captured events: normalise, dedupe by id, slice by time.

The event log stores whatever each producer sent, so replay works on a
canonical shape: an id, a known kind, an ISO timestamp and a payload dict.
Deduplication keeps the last version of an id, because a producer retry
carries the corrected payload.
"""

from shop.errors import ValidationError

__all__ = ["KINDS", "dedupe", "normalise", "summary", "window"]

KINDS = ("order.created", "order.paid", "order.shipped", "order.refunded", "stock.adjusted")


def normalise(event) -> dict:
    """A canonical event, refusing a missing id or an unknown kind."""
    source = dict(event or {})
    identifier = str(source.get("id", "")).strip()
    if not identifier:
        raise ValidationError("event needs an id")
    kind = str(source.get("kind", "")).strip()
    if kind not in KINDS:
        raise ValidationError("unknown event kind %r" % (kind,))
    at = str(source.get("at", "")).strip()
    if not at:
        raise ValidationError("event %r needs a timestamp" % (identifier,))
    return {"id": identifier, "kind": kind, "at": at, "payload": dict(source.get("payload") or {})}


def dedupe(events) -> list:
    """One event per id, the last one seen, first-seen order preserved."""
    order = []
    latest = {}
    for event in events:
        clean = normalise(event)
        if clean["id"] not in latest:
            order.append(clean["id"])
        latest[clean["id"]] = clean
    return [latest[identifier] for identifier in order]


def window(events, start: str, end: str) -> list:
    """Events whose timestamp falls inside the inclusive ISO window."""
    low = str(start)
    high = str(end)
    return [dict(event) for event in events if low <= str(event.get("at", "")) <= high]


def summary(events) -> dict:
    """Totals and per-kind counts for a replay run."""
    counts = {}
    total = 0
    for event in events:
        total += 1
        kind = str(event.get("kind", ""))
        counts[kind] = counts.get(kind, 0) + 1
    return {"total": total, "by_kind": dict(sorted(counts.items()))}
