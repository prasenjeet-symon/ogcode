"""Discovery credit: an account with no lifetime spend yet gets two
dollars back per distinct line, up to six dollars. It rewards trying several
things once rather than buying one expensive thing."""

RULE_ID = "R-190"
TITLE = "First basket discovery credit per line"
NEW_ACCOUNT_PER_LINE_CENTS = 200
NEW_ACCOUNT_MAX_LINES = 3


def applies(order: dict) -> bool:
    customer = order.get("customer") or {}
    rows = order.get("lines") or []
    return bool(rows) and int(customer.get("lifetime_cents") or 0) == 0


def discount_cents(order: dict) -> int:
    if not applies(order):
        return 0
    distinct = {str(row.get("sku") or "") for row in order.get("lines") or []}
    return min(len(distinct), NEW_ACCOUNT_MAX_LINES) * NEW_ACCOUNT_PER_LINE_CENTS
