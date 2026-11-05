"""granite-express rate card.

Granite Express charges by the hundred grams rather than the kilogram,
which is expensive for documents and honest for small hardware parts.
The unit count is returned so a dispute can be recomputed by hand."""

from shop.errors import ValidationError

CARRIER = "granite-express"
ZONE_RATES = {"hub": 455, "district": 605, "national": 880}
PER_100G_CENTS = {"hub": 12, "district": 15, "national": 19}
UNIT_G = 100
STATUS_MAP = {"registered": "label_created", "in_transit": "in_transit", "on_truck": "out_for_delivery", "delivered": "delivered", "exception": "exception", "returned": "returned"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel in hundred-gram units."""
    if zone not in ZONE_RATES:
        raise ValidationError("granite-express has no card for zone %r" % (zone,))
    units = max(1, -(-int(weight_g) // UNIT_G))
    cents = ZONE_RATES[zone] + units * PER_100G_CENTS[zone]
    return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "units": units}


def parse_tracking(payload: dict) -> dict:
    """Read a granite-express record, whose status nests under detail."""
    record = dict(payload or {})
    detail = record.get("detail") or {}
    code = str(detail.get("status") or "").strip().lower()
    events = [
        {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("status") or "").lower(), "unknown")}
        for step in record.get("history") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("id") or ""),
        "status": STATUS_MAP.get(code, "unknown"),
        "events": events,
    }
