"""List a product's variants, optionally filtered by one attribute.

Variants with no stock are kept in the answer but flagged, because the
size selector greys them out rather than hiding them."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "product_variants"
ROUTE = ('GET', '/api/products/{sku}/variants')



def handle(request: dict) -> dict:
    """List the variants of a product."""
    req = dict(request or {})
    parent = str(req.get("sku") or "").strip()
    if not parent:
        raise ValidationError("sku is required")
    wanted = req.get("attributes") or {}
    rows = []
    for variant in req.get("variants") or []:
        if str(variant.get("parent") or "") != parent:
            continue
        attributes = dict(variant.get("attributes") or {})
        if any(str(attributes.get(key)) != str(value) for key, value in wanted.items()):
            continue
        rows.append(
            {
                "sku": str(variant.get("sku")),
                "attributes": attributes,
                "in_stock": int(variant.get("stock") or 0) > 0,
            }
        )
    if not rows:
        raise NotFoundError("no variants for %s" % parent)
    rows.sort(key=lambda row: row["sku"])
    return {"variants": rows, "count": len(rows)}
