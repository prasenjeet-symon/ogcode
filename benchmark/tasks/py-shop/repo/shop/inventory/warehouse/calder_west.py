"""Calder West case-pack site.

Calder West ships in sealed cases of six. A customer asking for seven
units is quoted twelve, because breaking a case is a manual job that the
site charges for by the hour."""

WAREHOUSE = "calder-west"
STOCK = {"SKU-A1": 60, "SKU-H8": 17, "SKU-J2": 5}
CASE_PACK = 6


def stock_level(sku: str) -> int:
    """Whole units left at Calder West, cases included."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Round the line up to whole cases and allocate those."""
    wish = max(0, int(qty))
    cases = -(-wish // CASE_PACK) if wish else 0
    needed = cases * CASE_PACK
    have = int(STOCK.get(str(sku), 0))
    granted = needed if have >= needed else 0
    if granted:
        STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "case_pack": CASE_PACK,
        "cases": cases,
    }
