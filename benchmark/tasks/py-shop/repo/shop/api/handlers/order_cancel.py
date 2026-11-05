"""Cancel an order, but only while it is still cancellable.

Once a parcel is with the carrier the cancellation has to go through the
returns flow, so those statuses are refused here."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "order_cancel"
ROUTE = ('POST', '/api/orders/{order_id}/cancel')
LOCKED_STATUSES = ("shipped", "delivered", "cancelled", "refunded")


def handle(request: dict) -> dict:
    """Cancel an order that has not shipped."""
    req = dict(request or {})
    order_id = str(req.get("order_id") or "").strip()
    if not order_id:
        raise ValidationError("order_id is required")
    orders = [dict(row) for row in req.get("orders") or []]
    for row in orders:
        if str(row.get("order_id")) != order_id:
            continue
        status = str(row.get("status") or "pending")
        if status in LOCKED_STATUSES:
            raise ValidationError("a %s order cannot be cancelled" % status)
        row["status"] = "cancelled"
        return {"order": row, "previous": status}
    raise NotFoundError("no order %s" % order_id)
