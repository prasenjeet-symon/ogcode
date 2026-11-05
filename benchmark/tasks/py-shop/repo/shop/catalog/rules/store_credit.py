"""Store credit: a balance carried on the order is spent down, but never
past the thousand-cent cap that support may release without an approval,
and never past the subtotal itself."""

RULE_ID = "R-170"
TITLE = "Apply a customer's store credit"
CREDIT_RELEASE_CAP_CENTS = 1000


def applies(order: dict) -> bool:
    credit = int(order.get("store_credit_cents") or 0)
    subtotal = int(order.get("subtotal_cents") or 0)
    return credit > 0 and subtotal > 0


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    credit = int(order.get("store_credit_cents") or 0)
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(credit, CREDIT_RELEASE_CAP_CENTS, subtotal)
