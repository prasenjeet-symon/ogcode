"""Weekend promo: Saturday and Sunday orders take a flat credit once the
basket clears twenty dollars. The weekday comes from the order timestamp so
a replayed order always scores the same way."""

from datetime import date

RULE_ID = "R-180"
TITLE = "Weekend flat credit"
WEEKEND_DAYS = (5, 6)
WEEKEND_CENTS = 350
WEEKEND_MIN_SUBTOTAL = 2000


def _weekday(order: dict) -> int:
    stamp = str(order.get("created_at") or "")[:10]
    try:
        return date.fromisoformat(stamp).weekday()
    except ValueError:
        return -1


def applies(order: dict) -> bool:
    subtotal = int(order.get("subtotal_cents") or 0)
    return _weekday(order) in WEEKEND_DAYS and subtotal >= WEEKEND_MIN_SUBTOTAL


def discount_cents(order: dict) -> int:
    return WEEKEND_CENTS if applies(order) else 0
