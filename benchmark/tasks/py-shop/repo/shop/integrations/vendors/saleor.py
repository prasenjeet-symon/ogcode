"""Saleor order sync.

Orders leave Saleor in its own vocabulary -- unfulfilled, partially_fulfilled, fulfilled, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "saleor"
LABEL = "Saleor"
AUTH_HEADER = "X-Saleor-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 1000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('paid', 'fulfilling')
STATUS_ALIASES = {'unfulfilled': 'paid',
 'partially_fulfilled': 'fulfilling',
 'fulfilled': 'delivered',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'S-7210',
  'status': 'partially_fulfilled',
  'total_cents': 4875,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-02T09:03:00Z'},
 {'order_id': 'S-7211',
  'status': 'fulfilled',
  'total_cents': 1500,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-03T10:03:00Z'},
 {'order_id': 'S-7212',
  'status': 'canceled',
  'total_cents': 3125,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-04T11:03:00Z'},
 {'order_id': 'S-7213',
  'status': 'unfulfilled',
  'total_cents': 4750,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-05T12:03:00Z'}]


class Client:
    """Talk to a Saleor store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One row per order id, the last write winning."""
        latest = {}
        for row in self._orders:
            latest[row["order_id"]] = row
        return [dict(latest[order_id]) for order_id in sorted(latest)]

    def push_status(self, order_id: str, status: str) -> dict:
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
