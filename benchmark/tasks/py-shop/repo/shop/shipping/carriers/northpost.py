"""northpost rate card.

Northpost bills a zone base rate, then one rate per started kilogram, then
a flat handling fee. It ran the same-day pilot, so its numbers are the
ones finance reconciles against most often."""

from shop.errors import ValidationError

CARRIER = "northpost"
ZONE_RATES = {"local": 450, "regional": 620, "national": 780, "remote": 1150}
PER_KG_CENTS = {"local": 120, "regional": 145, "national": 170, "remote": 260}
HANDLING_CENTS = 75
STATUS_MAP = {"LABEL": "label_created", "PICKED_UP": "in_transit", "IN_TRANSIT": "in_transit", "OUT_FOR_DELIVERY": "out_for_delivery", "DELIVERED": "delivered", "EXCEPTION": "exception", "RETURNED": "returned"}


def quote(weight_g: int, zone: str) -> dict:
    """Price one parcel against the four zones northpost publishes."""
    if zone not in ZONE_RATES:
        raise ValidationError("northpost does not serve zone %r" % (zone,))
    if int(weight_g) <= 0:
        raise ValidationError("weight_g must be positive")
    kilos = int(weight_g) // 1000
    cents = ZONE_RATES[zone] + kilos * PER_KG_CENTS[zone] + HANDLING_CENTS
    return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "billable_g": int(weight_g)}


def parse_tracking(payload: dict) -> dict:
    """Map a northpost tracking document onto the house vocabulary."""
    record = dict(payload or {})
    code = str(record.get("status") or "").strip().upper()
    events = [
        {
            "at": step.get("timestamp"),
            "status": STATUS_MAP.get(str(step.get("status") or "").upper(), "unknown"),
        }
        for step in record.get("history") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("tracking_number") or ""),
        "status": STATUS_MAP.get(code, "unknown"),
        "events": events,
    }
