"""Coupon redemption.

An order may carry several coupons. The value column is the merchandise
the coupon was applied to, not the coupon's own discount, because the
discount engine owns that number."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Redemptions and touched value per coupon code."""
    usage = {}
    for order in orders or []:
        for code in order.get("coupon_codes") or []:
            entry = usage.setdefault(str(code), {"code": str(code), "orders": 0, "cents": 0})
            entry["orders"] += 1
            entry["cents"] += int(order.get("subtotal_cents") or 0)
    rows = [usage[key] for key in sorted(usage)]
    return {"report": "coupon_usage", "rows": rows, "distinct": len(rows)}
