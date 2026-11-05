"""Garrick Point.

Garrick Point caps every line at fifty units so one wholesale order
cannot empty the site in a single pick. Larger orders have to be split
across the network by the planner."""

WAREHOUSE = "garrick-point"
STOCK = {"SKU-A1": 120, "SKU-S8": 63, "SKU-T2": 9}
MAX_PER_LINE = 50


def stock_level(sku: str) -> int:
    """Units of ``sku`` at Garrick Point."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Allocate no more than the per-line cap."""
    wish = max(0, int(qty))
    capped = min(wish, MAX_PER_LINE)
    have = int(STOCK.get(str(sku), 0))
    granted = min(have, capped)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "capped": wish > MAX_PER_LINE,
    }
