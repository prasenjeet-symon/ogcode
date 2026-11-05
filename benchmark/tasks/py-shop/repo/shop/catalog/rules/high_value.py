"""High value: baskets above two hundred dollars take 5% back, capped
so a single furniture order does not wipe out the quarter. Only house-currency
orders qualify; converted orders are handled by finance offline."""

from shop.config import DEFAULT_CURRENCY

RULE_ID = "R-150"
TITLE = "High-value basket percentage"
HIGH_VALUE_MIN_CENTS = 20000
HIGH_VALUE_PERMILLE = 50
HIGH_VALUE_CAP_CENTS = 5000


def applies(order: dict) -> bool:
    if order.get("currency") != DEFAULT_CURRENCY:
        return False
    return int(order.get("subtotal_cents") or 0) >= HIGH_VALUE_MIN_CENTS


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * HIGH_VALUE_PERMILLE // 1000, HIGH_VALUE_CAP_CENTS)
