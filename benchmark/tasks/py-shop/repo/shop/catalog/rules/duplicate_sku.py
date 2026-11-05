"""Duplicate lines: the storefront occasionally splits one SKU across two
cart lines after a merge. We credit a quarter-dollar per surplus line
instead of failing the checkout."""

RULE_ID = "R-140"
TITLE = "Credit for a SKU split across lines"
DUPLICATE_CENTS = 250
DUPLICATE_CAP = 3


def _surplus(order: dict) -> int:
    seen = {}
    for row in order.get("lines") or []:
        sku = str(row.get("sku") or "")
        if sku:
            seen[sku] = seen.get(sku, 0) + 1
    return sum(count - 1 for count in seen.values() if count > 1)


def applies(order: dict) -> bool:
    return _surplus(order) > 0


def discount_cents(order: dict) -> int:
    return min(_surplus(order) * DUPLICATE_CENTS, DUPLICATE_CAP * DUPLICATE_CENTS)
