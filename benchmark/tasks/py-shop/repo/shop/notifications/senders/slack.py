"""Channel post, rendered as Block Kit blocks."""

from shop.errors import ValidationError

__all__ = ["CHANNEL", "send"]

CHANNEL = "slack"


def send(message: dict) -> dict:
    """Turn the message into the two blocks a channel post needs."""
    msg = dict(message or {})
    body = str(msg.get("body") or "").strip()
    if not body:
        raise ValidationError("slack needs a body")
    blocks = [
        {"type": "header", "text": {"type": "plain_text", "text": str(msg.get("subject") or CHANNEL)}},
        {"type": "section", "text": {"type": "mrkdwn", "text": body[:3000]}},
    ]
    return {
        "channel": CHANNEL,
        "target": str(msg.get("to") or "#operations"),
        "blocks": blocks,
        "accepted": True,
    }
