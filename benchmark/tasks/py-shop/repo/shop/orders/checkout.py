"""Turning a cart into a priced order."""

from shop.config import settings
from shop.discounts import stacking
from shop.discounts.tiers import tier_for_subtotal
from shop.money import allocate, mul_rate
from shop.orders.models import Order

# What the carrier charges below the free-shipping threshold.
FLAT_SHIPPING_CENTS = 499


def shipping_cents(subtotal_cents: int) -> int:
    """Free at or above the configured threshold, flat rate below it."""
    if int(subtotal_cents) >= settings.free_shipping_cents:
        return 0
    return FLAT_SHIPPING_CENTS


def checkout(cart, coupon_code=None, tax_rate_bps: int = 0) -> Order:
    """Price a cart.

    Tax is charged on the discounted subtotal, after every discount has been
    applied, and shipping is added after tax because the carrier is not a
    taxable line. Each line also carries the share of the discount that belongs
    to it, so the line totals add back up to the order total.
    """
    lines = list(cart.lines)
    subtotal = sum(line.subtotal_cents for line in lines)

    records = stacking.apply_discounts(subtotal, coupon_code)
    discount_total = stacking.total_discount_cents(records)

    tax = mul_rate(subtotal, tax_rate_bps)
    ship = shipping_cents(subtotal)
    total = subtotal - discount_total + ship + tax

    shares = allocate(discount_total, [line.subtotal_cents for line in lines])
    for line, share in zip(lines, shares):
        line.discount_cents = share

    return Order(
        subtotal_cents=subtotal,
        discount_cents=discount_total,
        shipping_cents=ship,
        tax_cents=tax,
        total_cents=total,
        tier=tier_for_subtotal(subtotal),
        discounts=records,
        lines=lines,
    )
