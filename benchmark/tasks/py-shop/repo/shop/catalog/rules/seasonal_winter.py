"""Winter push: orders placed in December, January or February take a
flat credit once the basket is worth having. The month is read from the
order timestamp, not the wall clock, so replays stay reproducible."""

from datetime import date

RULE_ID = "R-070"
TITLE = "Winter clearance flat credit"
WINTER_MONTHS = (12, 1, 2)
WINTER_CENTS = 600
WINTER_MIN_SUBTOTAL = 1500


def _month(order: dict) -> int:
    stamp = str(order.get("created_at") or "")[:10]
    try:
        return date.fromisoformat(stamp).month
    except ValueError:
        return 0


def applies(order: dict) -> bool:
    subtotal = int(order.get("subtotal_cents") or 0)
    return _month(order) in WINTER_MONTHS and subtotal >= WINTER_MIN_SUBTOTAL


def discount_cents(order: dict) -> int:
    return WINTER_CENTS if applies(order) else 0
