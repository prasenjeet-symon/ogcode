"""Home bundle: two separate home-and-living lines on one order earn
7% back on the home lines only. One line repeated does not count; the
merchandising team wants cross-sell, not bulk."""

RULE_ID = "R-050"
TITLE = "Home bundle when two home lines appear"
BUNDLE_CATEGORY = "home"
BUNDLE_MIN_LINES = 2
BUNDLE_PERMILLE = 70
BUNDLE_CAP_CENTS = 2500


def _home(order: dict) -> tuple:
    rows = [r for r in order.get("lines") or [] if str(r.get("category") or "") == BUNDLE_CATEGORY]
    cents = sum(int(r.get("qty") or 0) * int(r.get("unit_cents") or 0) for r in rows)
    return len(rows), cents


def applies(order: dict) -> bool:
    count, _cents = _home(order)
    return count >= BUNDLE_MIN_LINES


def discount_cents(order: dict) -> int:
    _count, cents = _home(order)
    return min(cents * BUNDLE_PERMILLE // 1000, BUNDLE_CAP_CENTS)
