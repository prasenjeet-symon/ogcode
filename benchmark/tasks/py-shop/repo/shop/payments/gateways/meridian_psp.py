"""Meridian PSP (vendor id ``meridian-psp``).

Meridian is a UK acquirer, so its statuses are spelled the British
way. The sandbox has been in title case since it launched.
"""

VENDOR = "meridian-psp"
SETTLEMENT_DAYS = 3
FEE_BPS = 155
REFUND_WINDOW_DAYS = 60
CURRENCIES = ("GBP", "EUR")

STATUS_MAP = {
    'Authorised': 'authorized',
    'Settled': 'captured',
    'Refused': 'failed',
    'Chargeback': 'disputed',
    'Pending': 'pending',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Meridian PSP sent us.

    Meridian refuses to lowercase anything, and `Refused` is its
    word for what everyone else calls declined.
    """
    key = str(status or "").strip()
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Meridian PSP lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Meridian PSP."""
    if amount_cents <= 0:
        return 0
    return (amount_cents * FEE_BPS + 9999) // 10000
