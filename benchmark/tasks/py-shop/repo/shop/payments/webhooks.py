"""Inbound gateway webhooks: signing, verification and dispatch.

Gateways sign the raw body, so the body is hashed before it is parsed. The
timestamp is checked against a five minute window; a replayed old signature is
rejected even though it verifies.
"""

import hashlib
import hmac
import time

from shop.errors import ValidationError

MAX_SKEW_SECONDS = 300
HANDLERS = {
    "payment.captured": "capture",
    "payment.refunded": "refund",
    "payment.failed": "fail",
    "chargeback.opened": "dispute",
}


def signature(secret: str, body: bytes, timestamp: int) -> str:
    """The hex signature a gateway with this secret would send."""
    message = str(int(timestamp)).encode("utf-8") + b"." + body
    return hmac.new(secret.encode("utf-8"), message, hashlib.sha256).hexdigest()


def verify(secret: str, body: bytes, timestamp, received: str, now: int = None) -> bool:
    """True when the signature matches and the timestamp is fresh."""
    try:
        moment = int(timestamp)
    except (TypeError, ValueError):
        return False
    clock = int(time.time()) if now is None else int(now)
    if abs(clock - moment) > MAX_SKEW_SECONDS:
        return False
    expected = signature(secret, body, moment)
    return hmac.compare_digest(expected, str(received or ""))


def dispatch(event_type: str, payload: dict) -> dict:
    """Route a verified event to the handler name the queue understands."""
    handler = HANDLERS.get(str(event_type or ""))
    if handler is None:
        raise ValidationError("no handler for event %r" % (event_type,))
    reference = str((payload or {}).get("reference") or "")
    if not reference:
        raise ValidationError("event %r carries no reference" % (event_type,))
    return {"handler": handler, "reference": reference, "event": event_type}


def handled_events() -> list:
    """Event types we accept, sorted, for the gateway integration docs."""
    return sorted(HANDLERS)
