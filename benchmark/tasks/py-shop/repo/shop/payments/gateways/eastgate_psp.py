"""Eastgate PSP (vendor id ``eastgate-psp``).

Eastgate renamed every status in 2022 and kept the old names in the
response for a year. Both spellings still show up in historical replays. It
does not allow API refunds at all.
"""

VENDOR = "eastgate-psp"
SETTLEMENT_DAYS = 3
FEE_BPS = 165
REFUND_WINDOW_DAYS = 0
MIN_FEE_CENTS = 20
LEGACY_MAP = {'ok': 'captured', 'bad': 'failed', 'hold': 'pending'}

STATUS_MAP = {
    'approved': 'captured',
    'rejected': 'failed',
    'reversed': 'voided',
    'waiting': 'pending',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Eastgate PSP sent us.

    The current vocabulary is tried first; anything left over is
    looked up in the pre-2022 table.
    """
    key = str(status or "").strip().lower()
    if key in STATUS_MAP:
        return STATUS_MAP[key]
    return LEGACY_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Eastgate PSP lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Eastgate PSP."""
    if amount_cents <= 0:
        return 0
    return max(amount_cents * FEE_BPS // 10000, MIN_FEE_CENTS)
