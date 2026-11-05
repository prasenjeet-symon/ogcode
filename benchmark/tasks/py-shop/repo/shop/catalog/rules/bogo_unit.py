"""Buy one get one: a line with two or more units gives away the cheapest
single unit among the qualifying lines. Lines priced at zero are ignored so
a free-sample row cannot become the giveaway."""

RULE_ID = "R-240"
TITLE = "Buy-one-get-one on a multi-unit line"
BOGO_MIN_QTY = 2


def _unit_prices(order: dict) -> list:
    prices = []
    for row in order.get("lines") or []:
        if int(row.get("qty") or 0) < BOGO_MIN_QTY:
            continue
        price = int(row.get("unit_cents") or 0)
        if price > 0:
            prices.append(price)
    return prices


def applies(order: dict) -> bool:
    return bool(_unit_prices(order))


def discount_cents(order: dict) -> int:
    prices = _unit_prices(order)
    return min(prices) if prices else 0
