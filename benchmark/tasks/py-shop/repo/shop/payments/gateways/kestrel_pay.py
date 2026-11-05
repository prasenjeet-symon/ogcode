"""Kestrel Pay (vendor id ``kestrel-pay``).

Kestrel appends a reason to the status with a colon, which is handy
in the portal and useless in our state machine.
"""

VENDOR = "kestrel-pay"
SETTLEMENT_DAYS = 1
FEE_BPS = 265
REFUND_WINDOW_DAYS = 45


STATUS_MAP = {
    'captured': 'captured',
    'declined': 'failed',
    'pending': 'pending',
    'refunded': 'refunded',
    'authorized': 'authorized',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Kestrel Pay sent us.

    Everything after the first colon is the human-readable reason,
    so only the head is looked up.
    """
    key = str(status or "").strip().split(":", 1)[0]
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Kestrel Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Kestrel Pay."""
    if amount_cents <= 0:
        return 0
    return (amount_cents * FEE_BPS + 9999) // 10000
