"""Availability arithmetic over a warehouse snapshot.

A snapshot is the shape the sync job writes: ``{"SKU-1": {"on_hand": 12,
"reserved": 2}}``. Nothing here mutates it; the callers own the write path.
"""

from shop.config import settings


def on_hand(snapshot: dict, sku: str) -> int:
    """Units physically in the building, zero for an unknown SKU."""
    row = snapshot.get(sku) or {}
    return int(row.get("on_hand") or 0)


def reserved(snapshot: dict, sku: str) -> int:
    """Units promised to carts that have not checked out yet."""
    row = snapshot.get(sku) or {}
    return int(row.get("reserved") or 0)


def sellable(snapshot: dict, sku: str) -> int:
    """Units that may still be sold. Never negative, however odd the feed."""
    return max(on_hand(snapshot, sku) - reserved(snapshot, sku), 0)


def is_low(snapshot: dict, sku: str, threshold: int = None) -> bool:
    """True when the sellable count is at or under the configured floor."""
    floor = settings.low_stock_threshold if threshold is None else threshold
    return sellable(snapshot, sku) <= floor


def restock_eta(days: int) -> str:
    """The phrasing the storefront shows for an inbound shipment."""
    if days <= 0:
        return "in stock"
    if days == 1:
        return "back tomorrow"
    return "back in %d days" % days


def shortage(snapshot: dict, wanted: dict) -> dict:
    """Per-SKU shortfall for a basket, empty when everything is coverable."""
    missing = {}
    for sku, qty in wanted.items():
        gap = int(qty) - sellable(snapshot, sku)
        if gap > 0:
            missing[sku] = gap
    return missing
