"""Quote carriage for a weight and zone, with the free-shipping floor.

The floor is read from the shop settings so the storefront banner and the
checkout total can never disagree."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "shipping_quote"
ROUTE = ('POST', '/api/shipping/quote')
from shop.config import settings

ZONE_TABLE = {"domestic": 499, "north": 749, "overseas": 1499}


def handle(request: dict) -> dict:
    """Quote a shipping price."""
    req = dict(request or {})
    zone = str(req.get("zone") or "domestic").strip().lower()
    if zone not in ZONE_TABLE:
        raise ValidationError("unknown zone %s" % zone)
    weight = int(req.get("weight_g") or 0)
    if weight <= 0:
        raise ValidationError("weight_g must be positive")
    bands = 1 + (weight - 1) // 1000
    base = ZONE_TABLE[zone]
    amount = base * bands
    subtotal = int(req.get("subtotal_cents") or 0)
    free_at = int(getattr(settings, "free_shipping_cents", 0) or 0)
    return {
        "zone": zone,
        "weight_g": weight,
        "bands": bands,
        "amount_cents": 0 if subtotal >= free_at else amount,
        "base_cents": amount,
    }
