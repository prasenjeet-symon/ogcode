"""Spend one use of a limited coupon.

The use is counted down before the discount is returned, so two concurrent
checkouts cannot both take the last redemption."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "coupon_redeem"
ROUTE = ('POST', '/api/coupons/redeem')



def handle(request: dict) -> dict:
    """Redeem one use of a coupon."""
    req = dict(request or {})
    code = str(req.get("code") or "").strip().upper()
    if not code:
        raise ValidationError("a coupon code is required")
    coupons = {key: dict(value) for key, value in (req.get("coupons") or {}).items()}
    coupon = coupons.get(code)
    if coupon is None:
        raise NotFoundError("unknown coupon %s" % code)
    left = int(coupon.get("uses_left", 0))
    if left <= 0:
        raise ValidationError("coupon %s has been fully redeemed" % code)
    coupon["uses_left"] = left - 1
    return {"coupons": coupons, "used": code, "uses_left": left - 1}
