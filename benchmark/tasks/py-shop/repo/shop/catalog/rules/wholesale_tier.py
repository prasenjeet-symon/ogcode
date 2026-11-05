"""Wholesale: accounts on wholesale terms take 10% off, capped at sixty
dollars, and only above a hundred-dollar opening order. The floor keeps
single-unit staff purchases off the wholesale schedule."""

RULE_ID = "R-270"
TITLE = "Wholesale account terms"
WHOLESALE_TIER = "wholesale"
WHOLESALE_PERMILLE = 100
WHOLESALE_MIN_CENTS = 10000
WHOLESALE_CAP_CENTS = 6000


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    tier = str(customer.get("tier") or "").strip().lower()
    subtotal = int(order.get("subtotal_cents") or 0)
    return tier == WHOLESALE_TIER and subtotal >= WHOLESALE_MIN_CENTS


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * WHOLESALE_PERMILLE // 1000, WHOLESALE_CAP_CENTS)
