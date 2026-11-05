"""Quote tax for a subtotal in a region, with the shop default as fallback.

Rates are stored in basis points so a 7.25% rate is an integer 725 and the
arithmetic never touches a float."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "tax_quote"
ROUTE = ('POST', '/api/tax/quote')
from shop.config import settings

REGION_RATES_BPS = {"US-CA": 725, "US-NY": 400, "GB": 2000, "DE": 1900}


def handle(request: dict) -> dict:
    """Quote sales tax for a region."""
    req = dict(request or {})
    subtotal = int(req.get("subtotal_cents") or 0)
    if subtotal < 0:
        raise ValidationError("subtotal_cents must not be negative")
    region = str(req.get("region") or "").strip().upper()
    fallback = int(getattr(settings, "tax_rate_bps", 0) or 0)
    rate = REGION_RATES_BPS.get(region, fallback)
    tax = (subtotal * rate + 5000) // 10000
    return {
        "region": region or "default",
        "rate_bps": rate,
        "subtotal_cents": subtotal,
        "tax_cents": tax,
        "total_cents": subtotal + tax,
    }
