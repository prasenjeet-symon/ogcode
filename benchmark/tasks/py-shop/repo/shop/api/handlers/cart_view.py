"""Total the cart against the price list the client sent back with it.

A line with no known price is reported in ``unpriced`` instead of being
silently counted at zero."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "cart_view"
ROUTE = ('GET', '/api/cart')



def handle(request: dict) -> dict:
    """Price up the cart."""
    req = dict(request or {})
    cart = [dict(line) for line in req.get("cart") or []]
    prices = req.get("prices") or {}
    subtotal = 0
    unpriced = []
    for line in cart:
        sku = str(line.get("sku"))
        if sku not in prices:
            unpriced.append(sku)
            continue
        subtotal += int(prices[sku]) * int(line.get("qty") or 0)
    return {
        "lines": len(cart),
        "units": sum(int(line.get("qty") or 0) for line in cart),
        "subtotal_cents": subtotal,
        "unpriced": unpriced,
        "empty": not cart,
    }
