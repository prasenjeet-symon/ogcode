"""Bracken cross-dock hub.

Bracken fills whatever it can and hands the remainder to the backorder
queue. It reports a fill rate so the planning team can see how badly a
promotion drained the shelves."""

WAREHOUSE = "bracken-hub"
STOCK = {"SKU-A1": 9, "SKU-E2": 31, "SKU-F4": 4, "SKU-G6": 0}


def stock_level(sku: str) -> int:
    """Units of ``sku`` physically at Bracken."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Fill as much of the line as the shelf allows."""
    have = max(0, int(STOCK.get(str(sku), 0)))
    wish = max(0, int(qty))
    granted = min(have, wish)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "fill_rate": round(granted / wish, 3) if wish else 1.0,
    }
