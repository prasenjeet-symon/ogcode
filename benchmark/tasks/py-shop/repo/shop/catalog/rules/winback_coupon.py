"""Win-back: the COMEBACK code is mailed to customers who have not ordered
in a year. It is a flat five dollars and it does not stack, because the
mailer tells the customer exactly what they are getting."""

RULE_ID = "R-260"
TITLE = "Win-back coupon for a lapsed customer"
WINBACK_CODE = "COMEBACK"
WINBACK_CENTS = 500


def applies(order: dict) -> bool:
    codes = {str(code).strip().upper() for code in order.get("coupon_codes") or []}
    return WINBACK_CODE in codes


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    return min(WINBACK_CENTS, int(order.get("subtotal_cents") or 0))
