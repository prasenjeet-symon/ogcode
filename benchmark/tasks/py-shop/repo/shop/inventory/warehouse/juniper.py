"""Juniper hazardous store.

Juniper holds the aerosol and solvent lines. Hazardous SKUs are capped
per order regardless of what is on the shelf, because the courier will
not carry more than that in one consignment."""

WAREHOUSE = "juniper"
STOCK = {"SKU-A1": 50, "SKU-Y1": 88, "SKU-Z4": 15}
HAZMAT_SKUS = frozenset({"SKU-Y1", "SKU-Z4"})
HAZMAT_CAP = 4


def stock_level(sku: str) -> int:
    """Units of ``sku`` at Juniper."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Apply the hazardous-goods cap before allocating."""
    wish = max(0, int(qty))
    hazmat = str(sku) in HAZMAT_SKUS
    cap = min(wish, HAZMAT_CAP) if hazmat else wish
    have = int(STOCK.get(str(sku), 0))
    granted = min(have, cap)
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "hazmat": hazmat,
    }
