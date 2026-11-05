"""Freight credit: a basket weighing five kilograms or more is expensive
to move, so we credit part of the freight back rather than let the basket
abandon at the shipping step."""

RULE_ID = "R-120"
TITLE = "Freight credit on a heavy basket"
HEAVY_MIN_GRAMS = 5000
FREIGHT_CREDIT_CENTS = 900


def _grams(order: dict) -> int:
    total = 0
    for row in order.get("lines") or []:
        total += int(row.get("qty") or 0) * int(row.get("weight_g") or 0)
    return total


def applies(order: dict) -> bool:
    return _grams(order) >= HEAVY_MIN_GRAMS


def discount_cents(order: dict) -> int:
    return FREIGHT_CREDIT_CENTS if applies(order) else 0
