"""App orders: two or more items placed in the mobile app take a flat
three dollars. The item floor exists so the credit never swallows a single
cheap accessory whole."""

RULE_ID = "R-280"
TITLE = "App order credit"
APP_CHANNEL = "app"
APP_MIN_ITEMS = 2
APP_CENTS = 300


def applies(order: dict) -> bool:
    if str(order.get("channel") or "") != APP_CHANNEL:
        return False
    return int(order.get("items_count") or 0) >= APP_MIN_ITEMS


def discount_cents(order: dict) -> int:
    return APP_CENTS if applies(order) else 0
