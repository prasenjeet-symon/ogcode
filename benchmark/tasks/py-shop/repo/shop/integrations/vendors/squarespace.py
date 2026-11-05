"""Squarespace order sync.

Orders leave Squarespace in its own vocabulary -- pending, fulfilled, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "squarespace"
LABEL = "Squarespace"
AUTH_HEADER = "X-Squarespace-Token"
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
ACTIVE_STATUSES = ('new', 'shipped')
STATUS_ALIASES = {'pending': 'new', 'fulfilled': 'shipped', 'canceled': 'cancelled'}

SEED = [{'order_id': 'S-7040',
  'status': 'fulfilled',
  'total_cents': 5000,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-02T12:12:00Z'},
 {'order_id': 'S-7041',
  'status': 'canceled',
  'total_cents': 1625,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-03T13:12:00Z'},
 {'order_id': 'S-7042',
  'status': 'pending',
  'total_cents': 3250,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-04T14:12:00Z'},
 {'order_id': 'S-7043',
  'status': 'fulfilled',
  'total_cents': 4875,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-05T15:12:00Z'}]


class Client:
    """Talk to a Squarespace store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One page per call; the cursor wraps around at the end."""
        start = self._cursor
        page = self._orders[start:start + PAGE_SIZE]
        self._cursor = 0 if start + PAGE_SIZE >= len(self._orders) else start + PAGE_SIZE
        return [dict(row) for row in page]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
