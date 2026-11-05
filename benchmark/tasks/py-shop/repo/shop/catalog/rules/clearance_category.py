"""Clearance: anything filed under the clearance category takes 15%
off its own line total. The credit is scoped to clearance lines, so mixing
a clearance t-shirt into a tools order does not discount the tools."""

RULE_ID = "R-160"
TITLE = "Clearance lines at fifteen percent"
CLEARANCE_CATEGORY = "clearance"
CLEARANCE_PERMILLE = 150
CLEARANCE_CAP_CENTS = 3000


def _clearance_cents(order: dict) -> int:
    total = 0
    for row in order.get("lines") or []:
        if str(row.get("category") or "") == CLEARANCE_CATEGORY:
            total += int(row.get("qty") or 0) * int(row.get("unit_cents") or 0)
    return total


def applies(order: dict) -> bool:
    return _clearance_cents(order) > 0


def discount_cents(order: dict) -> int:
    return min(_clearance_cents(order) * CLEARANCE_PERMILLE // 1000, CLEARANCE_CAP_CENTS)
