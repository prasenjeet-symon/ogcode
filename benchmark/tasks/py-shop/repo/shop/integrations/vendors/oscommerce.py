"""osCommerce order sync.

Orders leave osCommerce in its own vocabulary -- pending, preparing, delivered, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "oscommerce"
LABEL = "osCommerce"
AUTH_HEADER = "X-osCommerce-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 1250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling')
STATUS_ALIASES = {'pending': 'new',
 'preparing': 'fulfilling',
 'delivered': 'delivered',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'O-7220',
  'status': 'delivered',
  'total_cents': 5750,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-02T10:06:00Z'},
 {'order_id': 'O-7221',
  'status': 'canceled',
  'total_cents': 2375,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-03T11:06:00Z'},
 {'order_id': 'O-7222',
  'status': 'pending',
  'total_cents': 4000,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-04T12:06:00Z'},
 {'order_id': 'O-7223',
  'status': 'preparing',
  'total_cents': 5625,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-05T13:06:00Z'}]


class Client:
    """Talk to a osCommerce store without leaving the process."""

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
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
