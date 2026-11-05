"""HarborPay (vendor id ``harborpay``).

HarborPay publishes webhooks with a namespace prefix on every event
type, which leaks into the status field of its REST responses too.
"""

VENDOR = "harborpay"
SETTLEMENT_DAYS = 2
FEE_BPS = 210
REFUND_WINDOW_DAYS = 180
FLAT_FEE_CENTS = 15

STATUS_MAP = {
    'succeeded': 'captured',
    'authorized': 'authorized',
    'failed': 'failed',
    'refunded': 'refunded',
    'pending': 'pending',
    'reversed': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever HarborPay sent us.

    One of three namespaces may prefix the status, and only ever
    one, so the first matching prefix is stripped and the loop stops.
    """
    key = str(status or "").strip()
    for prefix in ("payment.", "charge.", "txn."):
        if key.startswith(prefix):
            key = key[len(prefix):]
            break
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether HarborPay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through HarborPay."""
    if amount_cents <= 0:
        return 0
    return FLAT_FEE_CENTS + amount_cents * FEE_BPS // 10000
