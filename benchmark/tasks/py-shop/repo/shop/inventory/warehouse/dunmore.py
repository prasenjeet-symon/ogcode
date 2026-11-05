"""Dunmore depot.

Dunmore keeps a safety buffer on every line and will never allocate into
it. The buffer exists because the depot also serves the walk-in trade,
and an empty shelf costs more than a late online order."""

WAREHOUSE = "dunmore-depot"
STOCK = {"SKU-A1": 25, "SKU-K5": 6, "SKU-L7": 40}
SAFETY_BUFFER = 5


def stock_level(sku: str) -> int:
    """Units of ``sku`` on hand, buffer included."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Allocate from the sellable stock, leaving the buffer alone."""
    have = int(STOCK.get(str(sku), 0))
    usable = max(0, have - SAFETY_BUFFER)
    wish = max(0, int(qty))
    granted = min(usable, wish)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "usable": usable,
        "buffer": SAFETY_BUFFER,
    }
