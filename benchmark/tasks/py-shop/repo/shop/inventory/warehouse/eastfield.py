"""Eastfield pallet site.

Eastfield stores everything on pallets of twenty-four and quotes in
whole pallets. It is the cheapest site per unit and the slowest per line,
which is why only wholesale orders route here."""

WAREHOUSE = "eastfield"
STOCK = {"SKU-M1": 240, "SKU-N4": 71, "SKU-P6": 24}
PALLET_SIZE = 24


def stock_level(sku: str) -> int:
    """Units of ``sku`` stacked at Eastfield."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Quote in whole pallets and allocate the pallet count."""
    wish = max(0, int(qty))
    pallets = -(-wish // PALLET_SIZE) if wish else 0
    units = pallets * PALLET_SIZE
    have = int(STOCK.get(str(sku), 0))
    granted = units if have >= units else 0
    if granted:
        STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "pallets": pallets,
    }
