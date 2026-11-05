"""One daily summary standing in for several notifications."""

from shop.errors import ValidationError

__all__ = ["CHANNEL", "send"]

CHANNEL = "digest"


def send(message: dict) -> dict:
    """Collapse a message, or a list of items, into one summary."""
    msg = dict(message or {})
    items = list(msg.get("items") or [])
    if not items:
        subject = str(msg.get("subject") or "").strip()
        if not subject:
            raise ValidationError("digest needs items or a subject")
        items = [subject]
    joined = "; ".join(str(item) for item in items)
    return {
        "channel": CHANNEL,
        "count": len(items),
        "summary": joined[:200],
        "accepted": True,
    }
