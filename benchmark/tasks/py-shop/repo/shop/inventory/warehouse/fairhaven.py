"""Fairhaven priority site.

Fairhaven holds the SKUs the marketing calendar promotes, and gives them
first call on stock. Everything else is filled at half the requested
quantity while a promotion is live."""

WAREHOUSE = "fairhaven"
STOCK = {"SKU-A1": 18, "SKU-Q3": 44, "SKU-R5": 7}
PRIORITY_SKUS = frozenset({"SKU-A1", "SKU-Q3"})
SECONDARY_DIVISOR = 2


def stock_level(sku: str) -> int:
    """Units of ``sku`` at Fairhaven."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Fill priority SKUs whole and everyone else at half."""
    wish = max(0, int(qty))
    have = int(STOCK.get(str(sku), 0))
    if str(sku) in PRIORITY_SKUS:
        cap = wish
    else:
        cap = wish // SECONDARY_DIVISOR
    granted = min(have, cap)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "priority": str(sku) in PRIORITY_SKUS,
    }
