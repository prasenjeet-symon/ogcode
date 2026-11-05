"""Pinegrove PSP (vendor id ``pinegrove-psp``).

Pinegrove still runs the 2016 feed for merchants who never migrated.
Statuses are uppercase with a hyphen separating an action from its outcome.
"""

VENDOR = "pinegrove-psp"
SETTLEMENT_DAYS = 3
FEE_BPS = 135
REFUND_WINDOW_DAYS = 30
MIN_FEE_CENTS = 10

STATUS_MAP = {
    'auth_ok': 'authorized',
    'auth_declined': 'failed',
    'captured_full': 'captured',
    'refund_partial': 'refunded',
    'refund_full': 'refunded',
    'void_ok': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Pinegrove PSP sent us.

    The legacy feed hyphenates and uppercases; folding hyphens to
    underscores gives one lookup table for both feeds.
    """
    key = str(status or "").strip().replace("-", "_").lower()
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Pinegrove PSP lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Pinegrove PSP."""
    if amount_cents <= 0:
        return 0
    return max(amount_cents * FEE_BPS // 10000, MIN_FEE_CENTS)
