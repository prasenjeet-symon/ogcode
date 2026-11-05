"""Look a customer up by id or by email.

Support works from whichever of the two the customer quoted on the phone, so
both are accepted and the answer is the same record."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "customer_get"
ROUTE = ('GET', '/api/customers/{customer_id}')



def handle(request: dict) -> dict:
    """Fetch a customer record."""
    req = dict(request or {})
    wanted = str(req.get("customer_id") or req.get("email") or "").strip().lower()
    if not wanted:
        raise ValidationError("customer_id or email is required")
    for row in req.get("customers") or []:
        if wanted in (str(row.get("id")).lower(), str(row.get("email")).lower()):
            return {"customer": dict(row)}
    raise NotFoundError("no customer %s" % wanted)
