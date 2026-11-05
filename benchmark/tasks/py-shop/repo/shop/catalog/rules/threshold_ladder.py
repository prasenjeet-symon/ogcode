"""Spend ladder: the basket earns the best rung it clears, and only that
rung. The rungs are walked from the top, so an eighty-dollar basket gets the
seventy-dollar rung rather than the sum of everything below it."""

RULE_ID = "R-250"
TITLE = "Spend ladder, one rung only"
LADDER = ((10000, 1000), (7000, 600), (5000, 300))


def applies(order: dict) -> bool:
    subtotal = int(order.get("subtotal_cents") or 0)
    return any(subtotal >= floor for floor, _cents in LADDER)


def discount_cents(order: dict) -> int:
    subtotal = int(order.get("subtotal_cents") or 0)
    for floor, cents in LADDER:
        if subtotal >= floor:
            return cents
    return 0
