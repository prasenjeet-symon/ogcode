"""skyways rate card.

Skyways is the air option. Every parcel pays weight at the published
per-kilogram rate, and two additions stack on top: a remote-address
surcharge and a heavy-parcel surcharge."""

from shop.errors import ValidationError

CARRIER = "skyways"
ZONE_RATES = {"domestic": 890, "islands": 1340, "continental": 1620}
PER_KG_CENTS = 240
REMOTE_ZONES = frozenset({"islands"})
REMOTE_SURCHARGE_CENTS = 375
HEAVY_G = 20000
HEAVY_SURCHARGE_CENTS = 650
STATUS_MAP = {"booked": "label_created", "manifested": "in_transit", "airborne": "in_transit", "landed": "out_for_delivery", "delivered": "delivered", "held": "exception"}


def quote(weight_g: int, zone: str) -> dict:
    """Price an air parcel and list the surcharges that applied."""
    if zone not in ZONE_RATES:
        raise ValidationError("skyways does not fly to zone %r" % (zone,))
    cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS // 1000
    applied = []
    if zone in REMOTE_ZONES:
        cents += REMOTE_SURCHARGE_CENTS
        applied.append("remote")
    if int(weight_g) > HEAVY_G:
        cents += HEAVY_SURCHARGE_CENTS
        applied.append("heavy")
    return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "surcharges": applied}


def parse_tracking(payload: dict) -> dict:
    """Read a skyways air waybill, whose legs carry the flight state."""
    record = dict(payload or {})
    code = str(record.get("flight_status") or "").strip().lower()
    events = [
        {"at": leg.get("time"), "status": STATUS_MAP.get(str(leg.get("status") or "").lower(), "unknown")}
        for leg in record.get("legs") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("awb") or ""),
        "status": STATUS_MAP.get(code, "unknown"),
        "events": events,
    }
