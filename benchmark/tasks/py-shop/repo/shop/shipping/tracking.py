"""The tracking vocabulary every carrier adapter normalises into."""

from shop.errors import ValidationError

__all__ = ["CANONICAL_STATUSES", "TERMINAL_STATUSES", "latest", "normalise", "timeline"]

CANONICAL_STATUSES = (
    "label_created",
    "in_transit",
    "out_for_delivery",
    "delivered",
    "exception",
    "returned",
)

TERMINAL_STATUSES = frozenset({"delivered", "returned"})

_ALIASES = {
    "in-transit": "in_transit",
    "intransit": "in_transit",
    "out-for-delivery": "out_for_delivery",
    "pre_transit": "label_created",
    "pending": "label_created",
    "failed": "exception",
    "failure": "exception",
}


def normalise(status: str) -> str:
    """Fold a carrier spelling onto the canonical status, or ``unknown``."""
    key = str(status or "").strip().lower()
    key = _ALIASES.get(key, key)
    return key if key in CANONICAL_STATUSES else "unknown"


def timeline(events: list) -> list:
    """Sort tracking events by timestamp, dropping entries without one."""
    kept = [dict(event) for event in events or [] if event.get("at")]
    kept.sort(key=lambda event: str(event["at"]))
    for event in kept:
        event["status"] = normalise(event.get("status"))
    return kept


def latest(events: list) -> str:
    """The last known status in a timeline, or ``unknown`` when empty."""
    ordered = timeline(events)
    if not ordered:
        return "unknown"
    return ordered[-1]["status"]
