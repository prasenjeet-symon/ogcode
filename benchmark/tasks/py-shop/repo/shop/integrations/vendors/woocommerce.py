"""WooCommerce order sync.

Orders leave WooCommerce in its own vocabulary -- pending, processing, completed, cancelled, refunded -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "woocommerce"
LABEL = "WooCommerce"
AUTH_HEADER = "X-WooCommerce-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 1250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'pending': 'new',
 'processing': 'paid',
 'completed': 'delivered',
 'cancelled': 'cancelled',
 'refunded': 'refunded'}

SEED = [{'order_id': 'W-7010',
  'status': 'processing',
  'total_cents': 2375,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-02T09:03:00Z'},
 {'order_id': 'W-7011',
  'status': 'completed',
  'total_cents': 4000,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-03T10:03:00Z'},
 {'order_id': 'W-7012',
  'status': 'cancelled',
  'total_cents': 5625,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-04T11:03:00Z'},
 {'order_id': 'W-7013',
  'status': 'refunded',
  'total_cents': 2250,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-05T12:03:00Z'}]


class Client:
    """Talk to a WooCommerce store without leaving the process."""

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
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
