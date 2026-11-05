"""Newsletter nudge: an account that accepted marketing mail and has not
yet ordered twice gets a flat credit. Older accounts are excluded because
they have already been counted."""

RULE_ID = "R-110"
TITLE = "Newsletter opt-in on a young account"
NEWSLETTER_CENTS = 400
NEWSLETTER_MAX_ORDERS = 1


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    if not customer.get("accepts_marketing"):
        return False
    return int(customer.get("orders_count") or 0) <= NEWSLETTER_MAX_ORDERS


def discount_cents(order: dict) -> int:
    return NEWSLETTER_CENTS if applies(order) else 0
