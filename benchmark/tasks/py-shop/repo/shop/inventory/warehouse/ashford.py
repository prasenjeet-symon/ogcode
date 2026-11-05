"""Ashford distribution centre.

Ashford is the oldest site and the only one that still refuses partial
picks: a line either ships complete or it does not ship at all. The
constraint comes from the site's pick-and-pack conveyor, not from policy."""

WAREHOUSE = "ashford-dc"
STOCK = {"SKU-A1": 42, "SKU-B7": 12, "SKU-C3": 0, "SKU-D9": 8}


def stock_level(sku: str) -> int:
    """Units of ``sku`` on the Ashford shelves."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Reserve a line, refusing anything the site cannot fill whole."""
    have = int(STOCK.get(str(sku), 0))
    wish = max(0, int(qty))
    granted = wish if have >= wish else 0
    if granted:
        STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "partial": granted != wish,
    }
