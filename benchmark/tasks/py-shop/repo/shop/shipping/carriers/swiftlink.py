"""swiftlink rate card.

Swiftlink sells parcels in half-kilogram blocks. A parcel is charged for
the blocks it occupies, rounded up, and anything over ten kilograms is
handed to the express network with a different rate band."""

from shop.errors import ValidationError

CARRIER = "swiftlink"
ZONE_RATES = {"metro": 520, "regional": 700, "national": 910}
BLOCK_CENTS = {"metro": 95, "regional": 125, "national": 160}
EXPRESS_BLOCKS = 20
STATUS_MAP = {"label_created": "label_created", "in_transit": "in_transit", "out_for_delivery": "out_for_delivery", "delivered": "delivered", "failed": "exception", "returned": "returned"}


def quote(weight_g: int, zone: str) -> dict:
    """Price one parcel by rounding its weight up to 500 g blocks."""
    if zone not in ZONE_RATES:
        raise ValidationError("swiftlink zone %r is not on the rate card" % (zone,))
    blocks = max(1, -(-int(weight_g) // 500))
    cents = ZONE_RATES[zone] + blocks * BLOCK_CENTS[zone]
    return {
        "carrier": CARRIER,
        "zone": zone,
        "amount_cents": cents,
        "blocks": blocks,
        "service": "express" if blocks > EXPRESS_BLOCKS else "standard",
    }


def parse_tracking(payload: dict) -> dict:
    """Read a swiftlink shipment document, which prefixes every state."""
    record = dict(payload or {})
    state = str(record.get("state") or "").strip().lower()
    if state.startswith("shipment."):
        state = state[len("shipment."):]
    events = []
    for step in record.get("checkpoints") or []:
        name = str(step.get("state") or "").strip().lower()
        events.append({"at": step.get("when"), "status": STATUS_MAP.get(name, "unknown")})
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("id") or ""),
        "status": STATUS_MAP.get(state, "unknown"),
        "events": events,
    }
