"""Add a SKU to a customer's wishlist without duplicating it.

The wishlist has a ceiling because the storefront renders all of it in one
scroll, and an unbounded list was the slowest page on the site."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "wishlist_add"
ROUTE = ('POST', '/api/wishlists/items')
MAX_WISHLIST = 100


def handle(request: dict) -> dict:
    """Add a SKU to a wishlist."""
    req = dict(request or {})
    customer_id = str(req.get("customer_id") or "").strip()
    sku = str(req.get("sku") or "").strip()
    if not customer_id or not sku:
        raise ValidationError("customer_id and sku are both required")
    items = [str(item) for item in req.get("items") or []]
    if sku in items:
        return {"items": items, "added": False, "reason": "already saved"}
    if len(items) >= MAX_WISHLIST:
        raise ValidationError("the wishlist already holds %d items" % MAX_WISHLIST)
    items.append(sku)
    return {"items": items, "added": True, "size": len(items)}
