"""Hold stock while the customer is in checkout.

The hold is all-or-nothing: a basket that cannot be filled in full is
refused here and the shopper is told which line is short."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "inventory_reserve"
ROUTE = ('POST', '/api/inventory/reserve')
HOLD_MINUTES = 20


def handle(request: dict) -> dict:
    """Reserve stock for a cart."""
    req = dict(request or {})
    reference = str(req.get("reference") or "").strip()
    if not reference:
        raise ValidationError("a reservation needs a reference")
    wanted = dict(req.get("wanted") or {})
    if not wanted:
        raise ValidationError("nothing to reserve")
    stock = req.get("stock") or {}
    short = {}
    for sku, qty in sorted(wanted.items()):
        if int(stock.get(sku, 0)) < int(qty):
            short[sku] = int(qty) - int(stock.get(sku, 0))
    if short:
        raise ValidationError("short on stock: %s" % ", ".join(sorted(short)))
    return {"reservation": {"reference": reference, "items": wanted, "minutes": HOLD_MINUTES}}
