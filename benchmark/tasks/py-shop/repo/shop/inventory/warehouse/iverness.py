"""Iverness weight-limited site.

Iverness sits at the end of a single-track road, so a van may only leave
with a fixed weight aboard. Allocation counts grams, not units, and stops
when the van is full."""

WAREHOUSE = "iverness"
STOCK = {"SKU-A1": 30, "SKU-W3": 12, "SKU-X5": 4}
UNIT_WEIGHT_G = {"SKU-A1": 700, "SKU-W3": 2100, "SKU-X5": 5400}
VAN_CAPACITY_G = 8000


def stock_level(sku: str) -> int:
    """Units of ``sku`` at Iverness."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Fill the van while its weight budget allows."""
    have = int(STOCK.get(str(sku), 0))
    wish = max(0, int(qty))
    unit = int(UNIT_WEIGHT_G.get(str(sku), 500))
    capacity = VAN_CAPACITY_G // unit if unit else 0
    granted = min(have, wish, capacity)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "weight_g": granted * unit,
        "capacity": capacity,
    }
