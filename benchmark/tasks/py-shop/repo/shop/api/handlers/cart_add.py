"""Add a line to the cart, merging into a line that is already there.

Quantities above the per-line ceiling are clamped rather than refused, because
the storefront stepper can race ahead of the server on a long key-press."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "cart_add"
ROUTE = ('POST', '/api/cart/items')
MAX_LINE_QTY = 25
MAX_CART_LINES = 40


def handle(request: dict) -> dict:
    """Add a line to the cart."""
    req = dict(request or {})
    sku = str(req.get("sku") or "").strip()
    if not sku:
        raise ValidationError("sku is required")
    qty = req.get("qty", 1)
    if not isinstance(qty, int) or isinstance(qty, bool) or qty < 1:
        raise ValidationError("qty must be a positive integer")
    cart = [dict(line) for line in req.get("cart") or []]
    for row in cart:
        if row.get("sku") == sku:
            row["qty"] = min(MAX_LINE_QTY, int(row.get("qty") or 0) + qty)
            break
    else:
        if len(cart) >= MAX_CART_LINES:
            raise ValidationError("the cart already holds %d lines" % MAX_CART_LINES)
        cart.append({"sku": sku, "qty": min(MAX_LINE_QTY, qty)})
    return {
        "cart": cart,
        "lines": len(cart),
        "units": sum(int(line["qty"]) for line in cart),
        "clamped": qty > MAX_LINE_QTY,
    }
