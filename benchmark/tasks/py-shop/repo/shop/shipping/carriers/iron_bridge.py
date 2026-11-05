"""iron-bridge rate card.

Iron Bridge bills in round five-cent steps, because its invoicing system
predates decimal currency and nobody has dared change the rounding. The
raw figure is returned alongside the rounded one for reconciliation."""

from shop.errors import ValidationError

CARRIER = "iron-bridge"
ZONE_RATES = {"yard": 425, "regional": 610, "national": 845, "territory": 1290}
PER_KG_CENTS = {"yard": 105, "regional": 130, "national": 165, "territory": 250}
ROUNDING_STEP = 5
STATUS_MAP = {"created": "label_created", "accepted": "in_transit", "in_transit": "in_transit", "with_courier": "out_for_delivery", "delivered": "delivered", "exception": "exception"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel and round the result up to the next five cents."""
    if zone not in ZONE_RATES:
        raise ValidationError("iron-bridge serves no zone named %r" % (zone,))
    raw = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
    rounded = -(-raw // ROUNDING_STEP) * ROUNDING_STEP
    return {"carrier": CARRIER, "zone": zone, "amount_cents": rounded, "raw_cents": raw}


def parse_tracking(payload: dict) -> dict:
    """Read an iron-bridge shipment, whose tracking id is nested."""
    record = dict(payload or {})
    shipment = record.get("shipment") or {}
    state = str(record.get("state") or "").strip().lower()
    events = [
        {"at": entry.get("at"), "status": STATUS_MAP.get(str(entry.get("state") or "").lower(), "unknown")}
        for entry in record.get("audit") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(shipment.get("tracking_number") or ""),
        "status": STATUS_MAP.get(state, "unknown"),
        "events": events,
    }
