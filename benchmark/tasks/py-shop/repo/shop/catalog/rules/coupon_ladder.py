"""Coupon ladder: the seasonal codes each carry their own rate and only
the most generous one on the order is applied. Codes are matched case
insensitively because the storefront lowercases them before sending."""

RULE_ID = "R-060"
TITLE = "Stackable coupon codes, best one wins"


COUPON_PERMILLE = {"SPRING10": 100, "SPRING15": 150, "SUMMER5": 50}


def _codes(order: dict) -> set:
    return {str(code).strip().upper() for code in order.get("coupon_codes") or []}


def applies(order: dict) -> bool:
    return bool(_codes(order) & set(COUPON_PERMILLE))


def discount_cents(order: dict) -> int:
    best = max((COUPON_PERMILLE[c] for c in _codes(order) if c in COUPON_PERMILLE), default=0)
    return int(order.get("subtotal_cents") or 0) * best // 1000
