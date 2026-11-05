"""Store a new address for a customer.

The country is upper-cased and length-checked here rather than at the
carrier, because a two-letter code is the only thing the rate table reads."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "address_create"
ROUTE = ('POST', '/api/addresses')
REQUIRED_FIELDS = ("line1", "city", "country")


def handle(request: dict) -> dict:
    """Create a shipping address."""
    req = dict(request or {})
    address = dict(req.get("address") or {})
    for field in REQUIRED_FIELDS:
        if not str(address.get(field) or "").strip():
            raise ValidationError("address.%s is required" % field)
    country = str(address["country"]).strip().upper()
    if len(country) != 2:
        raise ValidationError("country must be a two-letter code")
    address["country"] = country
    address["city"] = str(address["city"]).strip().title()
    return {"address": address, "customer_id": str(req.get("customer_id") or "")}
