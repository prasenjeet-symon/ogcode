"""Store pickup: a click-and-collect order skips the parcel network, so
the handling half of the shipping fee is credited back. Parcel orders keep
their full shipping charge."""

RULE_ID = "R-210"
TITLE = "Click-and-collect credit"
PICKUP_CHANNEL = "pickup"
PICKUP_CREDIT_CENTS = 500


def applies(order: dict) -> bool:
    return str(order.get("channel") or "") == PICKUP_CHANNEL


def discount_cents(order: dict) -> int:
    return PICKUP_CREDIT_CENTS if applies(order) else 0
