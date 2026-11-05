"""Basket mix: an order with four or more distinct lines earns 3% back, up
to the configured cart ceiling. It is a nudge towards discovery rather
than a real discount."""

from shop.config import settings

RULE_ID = "R-130"
TITLE = "Small basket-mix credit"
MIX_MIN_LINES = 4
MIX_PERMILLE = 30
MIX_CAP_CENTS = 1200


def applies(order: dict) -> bool:
    rows = order.get("lines") or []
    return MIX_MIN_LINES <= len(rows) <= settings.max_cart_lines


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * MIX_PERMILLE // 1000, MIX_CAP_CENTS)
