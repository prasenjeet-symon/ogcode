"""Loyalty tiers earn a percentage off the merchandise subtotal: gold
takes 8%, platinum takes 12%. The discount is capped in cents and only
applies to orders billed in the house currency."""

from shop.config import DEFAULT_CURRENCY

RULE_ID = "R-030"
TITLE = "Loyalty tier percentage"
TIER_PERMILLE = {"gold": 80, "platinum": 120}
TIER_CAP_CENTS = 4000


def _tier(order: dict) -> str:
    customer = order.get("customer") or {}
    return str(customer.get("tier") or "").strip().lower()


def applies(order: dict) -> bool:
    if order.get("currency") != DEFAULT_CURRENCY:
        return False
    return _tier(order) in TIER_PERMILLE


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    subtotal = int(order.get("subtotal_cents") or 0)
    return min(subtotal * TIER_PERMILLE[_tier(order)] // 1000, TIER_CAP_CENTS)
