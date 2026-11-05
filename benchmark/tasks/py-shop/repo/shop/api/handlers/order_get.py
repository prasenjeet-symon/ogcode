"""Look one order up in the set the caller supplied.

The handler works on the orders it is given rather than reaching for a
store, which keeps the fixture runnable with nothing but the stdlib."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "order_get"
ROUTE = ('GET', '/api/orders/{order_id}')



def handle(request: dict) -> dict:
    """Fetch one order."""
    req = dict(request or {})
    order_id = str(req.get("order_id") or "").strip()
    if not order_id:
        raise ValidationError("order_id is required")
    for row in req.get("orders") or []:
        if str(row.get("order_id")) == order_id:
            return {"order": dict(row)}
    raise NotFoundError("no order %s" % order_id)
