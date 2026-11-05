"""Welcome credit: a customer with no completed orders gets 10% off,
paid out of a fixed pot. The credit is the smaller of the pot and a tenth
of the subtotal, so a tiny basket does not get a free ride."""

RULE_ID = "R-020"
TITLE = "Welcome credit for a first order"
WELCOME_POT_CENTS = 500
WELCOME_MIN_SUBTOTAL = 2000


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    subtotal = int(order.get("subtotal_cents") or 0)
    return int(customer.get("orders_count") or 0) == 0 and subtotal >= WELCOME_MIN_SUBTOTAL


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    return min(WELCOME_POT_CENTS, int(order.get("subtotal_cents") or 0) // 10)
