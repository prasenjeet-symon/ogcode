"""Mobile push notification, title and body only."""

from shop.errors import ValidationError

__all__ = ["CHANNEL", "TITLE_CHARS", "send"]

CHANNEL = "push"
TITLE_CHARS = 60


def send(message: dict) -> dict:
    """Cut the title to what a lock screen shows."""
    msg = dict(message or {})
    title = str(msg.get("subject") or msg.get("title") or "").strip()
    if not title:
        raise ValidationError("push needs a title")
    body = str(msg.get("body") or "")
    return {
        "channel": CHANNEL,
        "title": title[:TITLE_CHARS],
        "preview": body[:120],
        "accepted": True,
        "collapsed": len(title) > TITLE_CHARS,
    }
