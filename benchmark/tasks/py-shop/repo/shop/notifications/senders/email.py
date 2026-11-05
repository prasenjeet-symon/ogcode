"""Transactional email through the receipt provider."""

import hashlib

from shop.errors import ValidationError

__all__ = ["CHANNEL", "REQUIRED", "send"]

CHANNEL = "email"
REQUIRED = ("to", "subject", "body")


def send(message: dict) -> dict:
    """Accept a message and answer with the provider's receipt."""
    msg = dict(message or {})
    missing = [key for key in REQUIRED if not msg.get(key)]
    if missing:
        raise ValidationError("email needs %s" % ", ".join(missing))
    recipient = str(msg["to"])
    if "@" not in recipient:
        raise ValidationError("not an email address: %r" % (recipient,))
    seed = "%s|%s" % (recipient, msg["subject"])
    digest = hashlib.sha1(seed.encode("utf-8")).hexdigest()
    return {
        "channel": CHANNEL,
        "to": recipient,
        "accepted": True,
        "message_id": "em-" + digest[:10],
    }
