"""Best-selling SKUs.

Ranked by units rather than by revenue, because the warehouse plans pick
faces by units and only the buyers care about value."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Units sold per SKU, heaviest first, truncated by ``limit``."""
    limit = int(kwargs.get("limit", 5))
    units = {}
    for order in orders or []:
        for row in order.get("lines") or []:
            sku = str(row.get("sku") or "")
            units[sku] = units.get(sku, 0) + int(row.get("qty") or 0)
    ranked = sorted(units.items(), key=lambda pair: (-pair[1], pair[0]))[: max(0, limit)]
    return {
        "report": "top_skus",
        "limit": limit,
        "rows": [{"sku": sku, "units": count} for sku, count in ranked],
    }
