"""Capture all or part of an authorization.

Partial capture is normal for a split shipment; capturing more than was
authorized is never allowed."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "payment_capture"
ROUTE = ('POST', '/api/payments/capture')



def handle(request: dict) -> dict:
    """Capture an authorization."""
    req = dict(request or {})
    auth = req.get("authorization") or {}
    code = str(auth.get("code") or "").strip()
    if not code:
        raise ValidationError("an authorization code is required")
    authorized = int(auth.get("amount_cents") or 0)
    amount = int(req.get("amount_cents") or authorized)
    if amount <= 0:
        raise ValidationError("amount_cents must be positive")
    if amount > authorized:
        raise ValidationError("cannot capture %d of a %d authorization" % (amount, authorized))
    return {
        "capture": {"authorization": code, "amount_cents": amount},
        "uncaptured_cents": authorized - amount,
    }
