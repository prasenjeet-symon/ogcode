"""Staff discount: an account flagged as staff takes 20% off, capped at
eighty dollars per order. The cap is deliberate; laptops and espresso
machines are the reason it exists."""

RULE_ID = "R-230"
TITLE = "Staff discount at twenty percent"
STAFF_PERMILLE = 200
STAFF_CAP_CENTS = 8000


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    return customer.get("employee") is True and int(order.get("subtotal_cents") or 0) > 0


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * STAFF_PERMILLE // 1000, STAFF_CAP_CENTS)
