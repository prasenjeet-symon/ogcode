"""Accept a return while the parcel is still inside the window.

The window runs from delivery, not from the order, because a pre-order can
be placed months before the box lands."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "return_create"
ROUTE = ('POST', '/api/returns')
RETURN_WINDOW_DAYS = 30


def handle(request: dict) -> dict:
    """Open a return inside the return window."""
    req = dict(request or {})
    order_id = str(req.get("order_id") or "").strip()
    if not order_id:
        raise ValidationError("order_id is required")
    items = list(req.get("items") or [])
    if not items:
        raise ValidationError("a return needs at least one line")
    days = int(req.get("days_since_delivery") or 0)
    if days < 0:
        raise ValidationError("days_since_delivery must not be negative")
    if days > RETURN_WINDOW_DAYS:
        raise ValidationError("the %d day return window has closed" % RETURN_WINDOW_DAYS)
    return {
        "return": {
            "order_id": order_id,
            "items": items,
            "reason": str(req.get("reason") or "unwanted"),
            "label": "RTN-%s" % order_id.replace("ORD-", ""),
        },
        "days_left": RETURN_WINDOW_DAYS - days,
    }
