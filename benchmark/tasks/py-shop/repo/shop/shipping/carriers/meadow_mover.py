"""meadow-mover rate card.

Meadow Mover is the rural carrier. It prices weight linearly and then
adds one oversize fee for anything above thirty kilograms, since those
parcels ride the flatbed rather than the van."""

from shop.errors import ValidationError

CARRIER = "meadow-mover"
ZONE_RATES = {"village": 395, "county": 520, "statewide": 760}
PER_KG_CENTS = {"village": 100, "county": 118, "statewide": 152}
OVERSIZE_G = 30000
OVERSIZE_FEE_CENTS = 1200
STATUS_MAP = {"booked": "label_created", "collected": "in_transit", "depot": "in_transit", "van": "out_for_delivery", "signed": "delivered", "damaged": "exception"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel and flag oversize handling."""
    if zone not in ZONE_RATES:
        raise ValidationError("meadow-mover does not reach zone %r" % (zone,))
    cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
    oversize = int(weight_g) > OVERSIZE_G
    if oversize:
        cents += OVERSIZE_FEE_CENTS
    return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "oversize": oversize}


def parse_tracking(payload: dict) -> dict:
    """Read a meadow-mover feed, which reports a current milestone."""
    record = dict(payload or {})
    events = [
        {
            "at": step.get("at"),
            "status": STATUS_MAP.get(str(step.get("milestone") or "").lower(), "unknown"),
        }
        for step in record.get("events") or []
    ]
    current = str(record.get("currentMilestone") or "").strip().lower()
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("trackingNumber") or ""),
        "status": STATUS_MAP.get(current, "unknown"),
        "events": events,
    }
