"""Free shipping: once the merchandise subtotal clears the configured
threshold the shipping line is credited back in full. The threshold is a
setting, not a constant, so finance can move it without a code change."""

from shop.config import settings

RULE_ID = "R-040"
TITLE = "Free shipping above the house threshold"


def applies(order: dict) -> bool:
    subtotal = int(order.get("subtotal_cents") or 0)
    shipping = int(order.get("shipping_cents") or 0)
    return shipping > 0 and subtotal >= settings.free_shipping_cents


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    return int(order.get("shipping_cents") or 0)
