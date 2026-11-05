"""Referral credit: a customer who arrived through a referral link gets a
flat thank-you once their basket is big enough to be worth shipping. The
credit can never exceed the subtotal."""

RULE_ID = "R-100"
TITLE = "Referral thank-you on the first paid basket"
REFERRAL_CENTS = 750
REFERRAL_MIN_SUBTOTAL = 2500


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    subtotal = int(order.get("subtotal_cents") or 0)
    return bool(str(customer.get("referred_by") or "").strip()) and subtotal >= REFERRAL_MIN_SUBTOTAL


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    return min(REFERRAL_CENTS, int(order.get("subtotal_cents") or 0))
