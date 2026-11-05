"""Subscription renewals carry 6% off the merchandise subtotal, capped
at nine dollars. The flag is set by the subscription service on the order
itself, so no customer lookup is needed here."""

RULE_ID = "R-220"
TITLE = "Subscription renewal percentage"
SUBSCRIPTION_PERMILLE = 60
SUBSCRIPTION_CAP_CENTS = 900


def applies(order: dict) -> bool:
    return order.get("subscription") is True and int(order.get("subtotal_cents") or 0) > 0


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * SUBSCRIPTION_PERMILLE // 1000, SUBSCRIPTION_CAP_CENTS)
