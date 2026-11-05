"""peak-parcel rate card.

Peak Parcel prices by weight band rather than by kilogram: a parcel pays
its band fee on top of the zone rate. The band list is short and fixed
because the carrier sells five box sizes and nothing else."""

from shop.errors import ValidationError

CARRIER = "peak-parcel"
ZONE_RATES = {"city": 410, "suburban": 560, "national": 830}
BANDS = ((1000, "light"), (5000, "standard"), (15000, "heavy"))
BAND_CENTS = {"light": 180, "standard": 460, "heavy": 980, "freight": 2100}
STATUS_MAP = {"picked": "in_transit", "moved": "in_transit", "arrived_at_hub": "in_transit", "on_van": "out_for_delivery", "delivered": "delivered", "lost": "exception"}


def _band(weight_g: int) -> str:
    for limit, name in BANDS:
        if weight_g <= limit:
            return name
    return "freight"


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel by looking up its weight band."""
    if zone not in ZONE_RATES:
        raise ValidationError("peak-parcel zone %r is unknown" % (zone,))
    band = _band(int(weight_g))
    return {
        "carrier": CARRIER,
        "zone": zone,
        "amount_cents": ZONE_RATES[zone] + BAND_CENTS[band],
        "band": band,
    }


def parse_tracking(payload: dict) -> dict:
    """Read a peak-parcel document, which lists milestones rather than states."""
    record = dict(payload or {})
    tracking = record.get("tracking") or {}
    events = [
        {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("label") or "").lower(), "unknown")}
        for step in record.get("milestones") or []
    ]
    last = events[-1]["status"] if events else "unknown"
    return {
        "carrier": CARRIER,
        "tracking_number": str(tracking.get("code") or ""),
        "status": last,
        "events": events,
    }
