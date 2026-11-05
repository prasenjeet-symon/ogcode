"""Update the handful of customer fields a person may edit themselves.

Anything else, tier included, is refused: a customer may opt out of
marketing but may not promote themselves."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "customer_update"
ROUTE = ('PATCH', '/api/customers/{customer_id}')
SELF_SERVICE_FIELDS = ("name", "phone", "accepts_marketing", "locale")


def handle(request: dict) -> dict:
    """Update a customer's own fields."""
    req = dict(request or {})
    customer = dict(req.get("customer") or {})
    if not customer.get("id"):
        raise ValidationError("the customer record needs an id")
    patch = dict(req.get("patch") or {})
    refused = sorted(key for key in patch if key not in SELF_SERVICE_FIELDS)
    if refused:
        raise ValidationError("these fields are not self-service: %s" % ", ".join(refused))
    customer.update(patch)
    return {"customer": customer, "changed": sorted(patch)}
