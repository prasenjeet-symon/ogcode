"""Check a coupon against the basket before the customer commits.

The answer names the reason a code does not apply, because 'invalid coupon'
is the single most common support ticket the store gets."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "coupon_validate"
ROUTE = ('POST', '/api/coupons/validate')



def handle(request: dict) -> dict:
    """Validate a coupon code."""
    req = dict(request or {})
    code = str(req.get("code") or "").strip().upper()
    if not code:
        raise ValidationError("a coupon code is required")
    table = req.get("coupons") or {}
    coupon = table.get(code)
    if coupon is None:
        return {"code": code, "valid": False, "reason": "unknown code"}
    subtotal = int(req.get("subtotal_cents") or 0)
    minimum = int(coupon.get("min_subtotal_cents") or 0)
    if subtotal < minimum:
        return {"code": code, "valid": False, "reason": "needs %d more cents" % (minimum - subtotal)}
    if not coupon.get("active", True):
        return {"code": code, "valid": False, "reason": "retired"}
    return {
        "code": code,
        "valid": True,
        "amount_cents": int(coupon.get("amount_cents") or 0),
        "percent_off": int(coupon.get("percent_off") or 0),
    }
