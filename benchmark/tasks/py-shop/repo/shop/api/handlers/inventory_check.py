"""Answer, per SKU, whether the basket can ship today.

The threshold comes from the shop settings, so the same handler keeps the
storefront honest when a merchant raises it before a sale."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "inventory_check"
ROUTE = ('POST', '/api/inventory/check')
from shop.config import settings

DEFAULT_WANTED = 1


def handle(request: dict) -> dict:
    """Report availability for a basket."""
    req = dict(request or {})
    wanted = dict(req.get("wanted") or {})
    if not wanted:
        raise ValidationError("wanted is required, as a sku to quantity map")
    stock = req.get("stock") or {}
    floor = int(getattr(settings, "low_stock_threshold", 0) or 0)
    lines = []
    for sku, qty in sorted(wanted.items()):
        have = int(stock.get(sku, 0))
        need = int(qty or DEFAULT_WANTED)
        lines.append(
            {
                "sku": sku,
                "wanted": need,
                "available": have,
                "short": max(0, need - have),
                "low": have <= floor,
            }
        )
    return {"lines": lines, "all_available": all(line["short"] == 0 for line in lines)}
