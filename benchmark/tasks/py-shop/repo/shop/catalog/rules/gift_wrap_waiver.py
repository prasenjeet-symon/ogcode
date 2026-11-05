"""Gift wrap: the wrapping fee rides on the order as its own field, and
we waive it when the customer asked for gift wrapping. Paper costs less
than the goodwill it buys at this volume."""

RULE_ID = "R-200"
TITLE = "Gift wrap fee waived"
GIFT_WRAP_CENTS = 250


def applies(order: dict) -> bool:
    return order.get("gift_wrap") is True and int(order.get("subtotal_cents") or 0) > 0


def discount_cents(order: dict) -> int:
    return GIFT_WRAP_CENTS if applies(order) else 0
