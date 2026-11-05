"""Volume break: a line ordered at five units or more earns 5% back.
The credit is computed per qualifying line and capped, so one very large
line cannot swallow the whole margin."""

RULE_ID = "R-010"
TITLE = "Volume break on a single bulk line"
BULK_MIN_QTY = 5
BULK_PERMILLE = 50
BULK_CAP_CENTS = 1500


def _bulk_cents(order: dict) -> int:
    total = 0
    for row in order.get("lines") or []:
        qty = int(row.get("qty") or 0)
        if qty >= BULK_MIN_QTY:
            total += qty * int(row.get("unit_cents") or 0)
    return total


def applies(order: dict) -> bool:
    return _bulk_cents(order) > 0


def discount_cents(order: dict) -> int:
    return min(_bulk_cents(order) // 1000 * BULK_PERMILLE, BULK_CAP_CENTS)
