"""nopCommerce order sync.

Orders leave nopCommerce in its own vocabulary -- pending, processing, complete, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "nopcommerce"
LABEL = "nopCommerce"
AUTH_HEADER = "X-nopCommerce-Token"
PAGE_SIZE = 10
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
 'complete': 'delivered',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'N-7150',
  'status': 'cancelled',
  'total_cents': 4625,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-02T13:45:00Z'},
 {'order_id': 'N-7151',
  'status': 'pending',
  'total_cents': 6250,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-03T14:45:00Z'},
 {'order_id': 'N-7152',
  'status': 'processing',
  'total_cents': 2875,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-04T15:45:00Z'},
 {'order_id': 'N-7153',
  'status': 'complete',
  'total_cents': 4500,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-05T16:45:00Z'}]


class Client:
    """Talk to a nopCommerce store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Only orders worth syncing, above the vendor minimum."""
        return [dict(row) for row in self._orders if int(row["total_cents"]) >= MIN_TOTAL_CENTS]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
