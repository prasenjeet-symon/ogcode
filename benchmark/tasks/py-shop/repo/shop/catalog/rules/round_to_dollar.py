"""Receipt tidiness: a subtotal whose cents part is at least 50 rounds up
to the next whole dollar at our expense. Anything smaller is left alone,
because giving away four cents looks petty on a receipt."""

RULE_ID = "R-090"
TITLE = "Round a ragged subtotal down to the dollar"
ROUND_AT_CENTS = 50
DOLLAR_CENTS = 100


def applies(order: dict) -> bool:
    return int(order.get("subtotal_cents") or 0) % DOLLAR_CENTS >= ROUND_AT_CENTS


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    return DOLLAR_CENTS - int(order.get("subtotal_cents") or 0) % DOLLAR_CENTS
