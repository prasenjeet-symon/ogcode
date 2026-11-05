"""How discounts stack on one order.

The rules the merchandising team signed off on:

1. a percentage coupon comes off the subtotal first;
2. the spend tier comes off whatever the coupon left;
3. a fixed coupon is taken off last and can never make the payable amount
   negative.

Percentages therefore compound: each one applies to the amount the previous
discounts left behind, never to the original subtotal.
"""

from shop.discounts import coupons
from shop.discounts.tiers import tier_for_subtotal, tier_rate_bps
from shop.money import mul_rate


def _record(kind: str, code: str, base_cents: int, rate_bps: int, amount_cents: int) -> dict:
    """One line of the discount breakdown, as it is shown to a customer."""
    return {
        "kind": kind,
        "code": code,
        "base_cents": base_cents,
        "rate_bps": rate_bps,
        "amount_cents": amount_cents,
    }


def apply_discounts(subtotal_cents: int, coupon_code=None) -> list[dict]:
    """The discount breakdown for a subtotal, in the order they were applied."""
    subtotal = int(subtotal_cents)
    records: list[dict] = []
    remaining = subtotal

    coupon = coupons.lookup(coupon_code) if coupon_code else None

    if coupon is not None and coupon.kind == "percent":
        amount = mul_rate(remaining, coupon.value)
        if amount:
            records.append(_record("coupon", coupon.code, remaining, coupon.value, amount))
            remaining -= amount

    rate = tier_rate_bps(subtotal)
    if rate:
        amount = mul_rate(remaining, rate)
        if amount:
            records.append(_record("tier", tier_for_subtotal(subtotal), remaining, rate, amount))
            remaining -= amount

    if coupon is not None and coupon.kind == "fixed":
        amount = min(coupon.value, remaining)
        if amount:
            records.append(_record("coupon", coupon.code, remaining, 0, amount))
            remaining -= amount

    return records


def total_discount_cents(records: list[dict]) -> int:
    """The sum of a breakdown's amounts."""
    return sum(int(record["amount_cents"]) for record in records)
