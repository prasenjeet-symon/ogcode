"""BigCommerce order sync.

Orders leave BigCommerce in its own vocabulary -- pending, awaiting_payment, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "bigcommerce"
LABEL = "BigCommerce"
AUTH_HEADER = "X-BigCommerce-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 1500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'pending', 'shipped')
STATUS_ALIASES = {'pending': 'new',
 'awaiting_payment': 'pending',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'B-7020',
  'status': 'shipped',
  'total_cents': 3250,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-02T10:06:00Z'},
 {'order_id': 'B-7021',
  'status': 'cancelled',
  'total_cents': 4875,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-03T11:06:00Z'},
 {'order_id': 'B-7022',
  'status': 'pending',
  'total_cents': 1500,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-04T12:06:00Z'},
 {'order_id': 'B-7023',
  'status': 'awaiting_payment',
  'total_cents': 3125,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-05T13:06:00Z'}]


class Client:
    """Talk to a BigCommerce store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Only orders that have not settled yet."""
        return [dict(row) for row in self._orders if row["status"] in ACTIVE_STATUSES]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
