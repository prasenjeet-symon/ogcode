"""Drop one SKU from the cart.

A line that is not in the cart is a 404 rather than a silent no-op, so a client that removed the wrong row hears about it."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "cart_remove"
ROUTE = ('DELETE', '/api/cart/items')



def handle(request: dict) -> dict:
    """Remove a line from the cart."""
    req = dict(request or {})
    sku = str(req.get("sku") or "").strip()
    if not sku:
        raise ValidationError("sku is required")
    cart = [dict(line) for line in req.get("cart") or []]
    kept = [line for line in cart if line.get("sku") != sku]
    if len(kept) == len(cart):
        raise NotFoundError("sku %s is not in the cart" % sku)
    return {"cart": kept, "removed": sku, "lines": len(kept)}
