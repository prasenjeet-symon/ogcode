"""Dunmore Pay (vendor id ``dunmore-pay``).

Dunmore names its statuses as full sentences that all begin with the
same word, so a prefix table is more honest than an exact map.
"""

VENDOR = "dunmore-pay"
SETTLEMENT_DAYS = 4
FEE_BPS = 145
REFUND_WINDOW_DAYS = 30
FLAT_FEE_CENTS = 30
PREFIXES = (
    ("payment captured", "captured"),
    ("payment declined", "failed"),
    ("payment refunded", "refunded"),
    ("payment authorised", "authorized"),
    ("payment pending", "pending"),
)

STATUS_MAP = {
    # no exact table: the matcher below owns the vocabulary
}


def normalize(status: str) -> str:
    """The canonical status for whatever Dunmore Pay sent us.

    The table is ordered longest-first, so `payment captured`
    wins over any shorter prefix that happens to share its opening.
    """
    key = str(status or "").strip().lower()
    for prefix, canonical in PREFIXES:
        if key.startswith(prefix):
            return canonical
    return "unknown"


def supports_refund() -> bool:
    """Whether Dunmore Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Dunmore Pay."""
    if amount_cents <= 0:
        return 0
    return FLAT_FEE_CENTS + amount_cents * FEE_BPS // 10000
