"""Cobalt PSP (vendor id ``cobalt-psp``).

Cobalt embeds the status in a nested object that the webhook and the
polling endpoint shape differently. Both end up here.
"""

VENDOR = "cobalt-psp"
SETTLEMENT_DAYS = 2
FEE_BPS = 225
REFUND_WINDOW_DAYS = 120


STATUS_MAP = {
    'CAPTURED': 'captured',
    'DECLINED': 'failed',
    'AUTHORIZED': 'authorized',
    'REFUNDED': 'refunded',
    'VOIDED': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Cobalt PSP sent us.

    A dict is unwrapped before the lookup, so a caller may hand us
    either the whole event or just the status string.
    """
    raw = status
    if isinstance(raw, dict):
        raw = raw.get("status", raw.get("state", ""))
    return STATUS_MAP.get(str(raw or "").strip().upper(), "unknown")


def supports_refund() -> bool:
    """Whether Cobalt PSP lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Cobalt PSP."""
    if amount_cents <= 0:
        return 0
    return (amount_cents * FEE_BPS + 9999) // 10000
