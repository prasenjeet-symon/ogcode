"""Lowmoor two-bin site.

Lowmoor runs two bins per SKU. When the live bin falls below its
threshold the site swaps bins and raises a restock note, which is how the
network notice board fills up overnight."""

WAREHOUSE = "lowmoor"
STOCK = {"SKU-A1": 14, "SKU-DD3": 9, "SKU-EE5": 31}
LOW_WATER = 10


def stock_level(sku: str) -> int:
    """Units of ``sku`` across both of Lowmoor's bins."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Allocate and raise a restock note when the bin runs low."""
    have = int(STOCK.get(str(sku), 0))
    wish = max(0, int(qty))
    granted = min(have, wish)
    left = have - granted
    STOCK[str(sku)] = left
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "remaining": left,
        "restock": left < LOW_WATER,
    }
