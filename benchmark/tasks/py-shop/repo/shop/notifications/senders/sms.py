"""Short message delivery, split into 160-character parts."""

from shop.errors import ValidationError

__all__ = ["CHANNEL", "PART_CHARS", "send"]

CHANNEL = "sms"
PART_CHARS = 160


def send(message: dict) -> dict:
    """Send a text, reporting how many parts it needed."""
    msg = dict(message or {})
    recipient = str(msg.get("to") or "")
    digits = "".join(char for char in recipient if char.isdigit())
    if len(digits) < 7:
        raise ValidationError("not a phone number: %r" % (recipient,))
    body = str(msg.get("body") or msg.get("subject") or "")
    if not body:
        raise ValidationError("sms needs a body")
    parts = max(1, -(-len(body) // PART_CHARS))
    return {
        "channel": CHANNEL,
        "to": "+" + digits,
        "accepted": True,
        "parts": parts,
        "truncated": len(body) > PART_CHARS,
    }
