"""SellerCloud order sync.

Orders leave SellerCloud in its own vocabulary -- new, processing, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "sellercloud"
LABEL = "SellerCloud"
AUTH_HEADER = "X-SellerCloud-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 2000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling', 'shipped')
STATUS_ALIASES = {'new': 'new',
 'processing': 'fulfilling',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'S-7390',
  'status': 'cancelled',
  'total_cents': 5625,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-02T17:57:00Z'},
 {'order_id': 'S-7391',
  'status': 'new',
  'total_cents': 2250,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-03T08:57:00Z'},
 {'order_id': 'S-7392',
  'status': 'processing',
  'total_cents': 3875,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-04T09:57:00Z'},
 {'order_id': 'S-7393',
  'status': 'shipped',
  'total_cents': 5500,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-05T10:57:00Z'}]


class Client:
    """Talk to a SellerCloud store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """The queue in arrival order, each row numbered."""
        return [dict(row, position=index) for index, row in enumerate(self._orders, start=1)]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status and remember the move for the audit trail."""
        previous = None
        for row in self._orders:
            if row["order_id"] == order_id:
                previous = row["status"]
                row["status"] = status
                break
        else:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self.history.append({"order_id": order_id, "from": previous, "to": status})
        return {"order_id": order_id, "status": status, "previous": previous, "moves": len(self.history)}
