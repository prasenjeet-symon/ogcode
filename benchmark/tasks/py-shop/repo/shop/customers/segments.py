"""Derived customer segments.

A segment is computed from what the customer has done, never stored on
the record. That keeps the marketing export honest: a customer cannot
be left in a stale segment because a nightly job did not run.
"""

from shop.config import TIER_BOUNDARIES

__all__ = ["SEGMENTS", "segment_for", "summarise"]

SEGMENTS = ("new", "dormant", "returning", "loyal", "vip")

LOYAL_ORDERS = 5
VIP_LIFETIME_CENTS = 100000
DORMANT_DAYS = 180
VIP_TIER = TIER_BOUNDARIES[-1]


def _orders(customer: dict) -> int:
    return int(customer.get("orders_count") or 0)


def segment_for(customer: dict) -> str:
    """One of ``SEGMENTS`` for this customer record."""
    record = customer or {}
    orders = _orders(record)
    if orders == 0:
        return "new"
    if int(record.get("days_since_order") or 0) >= DORMANT_DAYS:
        return "dormant"
    lifetime = int(record.get("lifetime_cents") or 0)
    if str(record.get("tier") or "") == VIP_TIER and lifetime >= VIP_LIFETIME_CENTS:
        return "vip"
    if orders >= LOYAL_ORDERS:
        return "loyal"
    return "returning"


def summarise(customers: list) -> dict:
    """How many customers landed in each segment."""
    counts = {name: 0 for name in SEGMENTS}
    for customer in customers or []:
        counts[segment_for(customer)] += 1
    return {"segments": counts, "customers": sum(counts.values())}
