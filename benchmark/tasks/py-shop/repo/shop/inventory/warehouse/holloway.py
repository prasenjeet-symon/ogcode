"""Holloway multi-bin site.

Holloway tracks stock per bin and drains the oldest bin first, so the
counts here are lists rather than integers. Emptying a bin removes it,
which keeps the site's pick lists short."""

WAREHOUSE = "holloway"
STOCK = {"SKU-A1": [4, 9, 2], "SKU-U6": [12], "SKU-V8": []}


def stock_level(sku: str) -> int:
    """Total units across every bin holding ``sku``."""
    return sum(int(count) for count in STOCK.get(str(sku)) or [])


def allocate(sku: str, qty: int) -> dict:
    """Drain the oldest bin first and report the bins used."""
    bins = STOCK.setdefault(str(sku), [])
    wish = max(0, int(qty))
    granted = 0
    used = 0
    while bins and granted < wish:
        oldest = int(bins[0])
        take = min(oldest, wish - granted)
        granted += take
        used += 1
        if take >= oldest:
            bins.pop(0)
        else:
            bins[0] = oldest - take
    return {
        "warehouse": WAREHOUSE,
        "sku": str(sku),
        "requested": wish,
        "granted": granted,
        "backorder": wish - granted,
        "bins_used": used,
    }
