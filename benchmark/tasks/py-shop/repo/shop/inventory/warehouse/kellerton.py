"""Kellerton batch site.

Kellerton will not open a batch for fewer than three units, so small
orders are refused rather than picked. The rule keeps the batch records
clean for the traceability audit."""

WAREHOUSE = "kellerton"
STOCK = {"SKU-A1": 22, "SKU-BB2": 11, "SKU-CC7": 2}
MIN_BATCH = 3


def stock_level(sku: str) -> int:
    """Units of ``sku`` at Kellerton."""
    return int(STOCK.get(str(sku), 0))


def allocate(sku: str, qty: int) -> dict:
    """Refuse a line smaller than one whole batch."""
    wish = max(0, int(qty))
    have = int(STOCK.get(str(sku), 0))
    granted = min(have, wish) if wish >= MIN_BATCH else 0
    STOCK[str(sku)] = have - granted
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "min_batch": MIN_BATCH,
    }
