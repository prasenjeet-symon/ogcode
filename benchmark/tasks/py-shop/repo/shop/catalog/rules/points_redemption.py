"""Points redemption: two points buy one cent, spent in whole hundreds
of cents so the receipt never shows a ragged number. Whatever is left
over stays on the account."""

RULE_ID = "R-080"
TITLE = "Spend loyalty points in whole hundreds"
POINTS_PER_CENT = 2
REDEEM_STEP_CENTS = 100
REDEEM_CAP_CENTS = 800


def _points(order: dict) -> int:
    customer = order.get("customer") or {}
    return int(customer.get("points") or 0)


def applies(order: dict) -> bool:
    return _points(order) // POINTS_PER_CENT >= REDEEM_STEP_CENTS


def discount_cents(order: dict) -> int:
    spendable = _points(order) // POINTS_PER_CENT
    return min(spendable // REDEEM_STEP_CENTS * REDEEM_STEP_CENTS, REDEEM_CAP_CENTS)
