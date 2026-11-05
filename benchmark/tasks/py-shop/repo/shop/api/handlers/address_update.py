"""Patch an address with only the fields the client changed.

Unknown fields are refused instead of stored, so a typo in a client cannot
quietly add a column to every address record."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "address_update"
ROUTE = ('PATCH', '/api/addresses/{address_id}')
EDITABLE = ("line1", "line2", "city", "region", "postcode", "country")


def handle(request: dict) -> dict:
    """Patch one field of an address."""
    req = dict(request or {})
    address = dict(req.get("address") or {})
    if not address:
        raise NotFoundError("no address to patch")
    patch = dict(req.get("patch") or {})
    if not patch:
        raise ValidationError("the patch is empty")
    for field in patch:
        if field not in EDITABLE:
            raise ValidationError("%s is not an editable address field" % field)
    address.update({key: str(value).strip() for key, value in patch.items()})
    address["country"] = address.get("country", "").upper()
    return {"address": address, "changed": sorted(patch)}
