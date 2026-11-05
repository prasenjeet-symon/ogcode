"""OpenCart order sync.

Orders leave OpenCart in its own vocabulary -- pending, processing, shipped, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "opencart"
LABEL = "OpenCart"
AUTH_HEADER = "X-OpenCart-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 1750
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
 'processing': 'paid',
 'shipped': 'shipped',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'O-7100',
  'status': 'shipped',
  'total_cents': 5250,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-02T08:30:00Z'},
 {'order_id': 'O-7101',
  'status': 'canceled',
  'total_cents': 1875,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-03T09:30:00Z'},
 {'order_id': 'O-7102',
  'status': 'pending',
  'total_cents': 3500,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-04T10:30:00Z'},
 {'order_id': 'O-7103',
  'status': 'processing',
  'total_cents': 5125,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-05T11:30:00Z'}]


class Client:
    """Talk to a OpenCart store without leaving the process."""

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
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
