"""Shopify order sync.

Orders leave Shopify in its own vocabulary -- pending, paid, fulfilled, cancelled, refunded -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "shopify"
LABEL = "Shopify"
AUTH_HEADER = "X-Shopify-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 1000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'pending': 'new',
 'paid': 'paid',
 'fulfilled': 'shipped',
 'cancelled': 'cancelled',
 'refunded': 'refunded'}

SEED = [{'order_id': 'S-7000',
  'status': 'pending',
  'total_cents': 1500,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-02T08:00:00Z'},
 {'order_id': 'S-7001',
  'status': 'paid',
  'total_cents': 3125,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-03T09:00:00Z'},
 {'order_id': 'S-7002',
  'status': 'fulfilled',
  'total_cents': 4750,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-04T10:00:00Z'},
 {'order_id': 'S-7003',
  'status': 'cancelled',
  'total_cents': 6375,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-05T11:00:00Z'}]


class Client:
    """Talk to a Shopify store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Newest first, cut to one page."""
        rows = sorted(self._orders, key=lambda row: row["created_at"], reverse=True)
        return [dict(row) for row in rows[:PAGE_SIZE]]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
