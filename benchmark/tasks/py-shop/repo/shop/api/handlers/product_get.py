"""Fetch a product by SKU.

Retired products stay reachable so order history can still render them, but
they are marked unavailable so the buy button can be withdrawn."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "product_get"
ROUTE = ('GET', '/api/products/{sku}')



def handle(request: dict) -> dict:
    """Fetch one product."""
    req = dict(request or {})
    sku = str(req.get("sku") or "").strip().upper()
    if not sku:
        raise ValidationError("sku is required")
    for product in req.get("products") or []:
        if str(product.get("sku") or "").upper() == sku:
            row = dict(product)
            row["purchasable"] = not row.get("retired", False) and int(row.get("stock") or 0) > 0
            return {"product": row}
    raise NotFoundError("no product %s" % sku)
