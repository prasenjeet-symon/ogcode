"""Authorize an amount against a payment method.

The authorization code is deterministic on the reference, which is what lets
the retry queue re-send an authorization without double-charging."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "payment_authorize"
ROUTE = ('POST', '/api/payments/authorize')
import hashlib

SUPPORTED_CURRENCIES = ("USD", "EUR", "GBP", "CAD")


def handle(request: dict) -> dict:
    """Authorize a card payment."""
    req = dict(request or {})
    reference = str(req.get("reference") or "").strip()
    if not reference:
        raise ValidationError("reference is required")
    amount = int(req.get("amount_cents") or 0)
    if amount <= 0:
        raise ValidationError("amount_cents must be positive")
    currency = str(req.get("currency") or "USD").upper()
    if currency not in SUPPORTED_CURRENCIES:
        raise ValidationError("currency %s is not supported" % currency)
    code = hashlib.sha1((reference + currency).encode("utf-8")).hexdigest()[:8].upper()
    return {
        "authorization": {
            "reference": reference,
            "code": "AUTH-" + code,
            "amount_cents": amount,
            "currency": currency,
            "state": "authorized",
        }
    }
