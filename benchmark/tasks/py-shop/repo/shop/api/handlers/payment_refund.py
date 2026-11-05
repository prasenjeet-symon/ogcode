"""Refund against a capture, never against more than was captured.

The answer says how much is still refundable, which is the number support
quotes back to the customer."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "payment_refund"
ROUTE = ('POST', '/api/payments/refund')



def handle(request: dict) -> dict:
    """Refund a captured payment."""
    req = dict(request or {})
    capture = req.get("capture") or {}
    if not capture.get("authorization"):
        raise NotFoundError("no capture to refund")
    captured = int(capture.get("amount_cents") or 0)
    already = int(capture.get("refunded_cents") or 0)
    amount = int(req.get("amount_cents") or (captured - already))
    if amount <= 0:
        raise ValidationError("nothing left to refund")
    if amount + already > captured:
        raise ValidationError("refund exceeds the %d cents captured" % captured)
    return {
        "refund": {"authorization": str(capture["authorization"]), "amount_cents": amount},
        "refundable_cents": captured - already - amount,
    }
