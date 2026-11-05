"""Signed POST to a partner endpoint, queued not delivered."""

import hashlib
import json

from shop.errors import ValidationError

__all__ = ["CHANNEL", "send"]

CHANNEL = "webhook"


def send(message: dict) -> dict:
    """Sign the payload and report the key ordering used."""
    msg = dict(message or {})
    if not msg:
        raise ValidationError("webhook needs a payload")
    payload = json.dumps(msg, sort_keys=True, separators=(",", ":"))
    signature = hashlib.sha256(payload.encode("utf-8")).hexdigest()
    return {
        "channel": CHANNEL,
        "accepted": True,
        "signature": signature[:16],
        "bytes": len(payload.encode("utf-8")),
        "keys": sorted(msg),
    }
