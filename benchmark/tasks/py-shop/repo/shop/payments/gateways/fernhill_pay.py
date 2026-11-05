"""Fernhill Pay (vendor id ``fernhill-pay``).

Fernhill runs its own spelling for two statuses and aliases them at
the edge. The alias table is the only place it happens.
"""

VENDOR = "fernhill-pay"
SETTLEMENT_DAYS = 1
FEE_BPS = 250
REFUND_WINDOW_DAYS = 90
ALIASES = {"authorised": "authorized", "canceled": "voided", "capture_pending": "pending"}
CANONICAL = ("authorized", "captured", "failed", "pending", "refunded", "voided", "disputed", "unknown")

STATUS_MAP = {
    'succeeded': 'captured',
    'authorised': 'authorized',
    'canceled': 'voided',
    'capture_pending': 'pending',
    'declined': 'failed',
    'refunded': 'refunded',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Fernhill Pay sent us.

    Aliases are folded after the primary lookup so the map itself
    can keep the spellings Fernhill actually sends.
    """
    key = str(status or "").strip().lower()
    canonical = STATUS_MAP.get(key, key)
    return ALIASES.get(canonical, canonical if canonical in CANONICAL else "unknown")


def supports_refund() -> bool:
    """Whether Fernhill Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Fernhill Pay."""
    if amount_cents <= 0:
        return 0
    return amount_cents * FEE_BPS // 10000
