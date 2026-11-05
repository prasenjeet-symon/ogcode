"""Big Cartel order sync.

Orders leave Big Cartel in its own vocabulary -- pending, processed, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "big_cartel"
LABEL = "Big Cartel"
AUTH_HEADER = "X-Big Cartel-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 2250
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
 'processed': 'paid',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'BC-7120',
  'status': 'pending',
  'total_cents': 2000,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-02T10:36:00Z'},
 {'order_id': 'BC-7121',
  'status': 'processed',
  'total_cents': 3625,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-03T11:36:00Z'},
 {'order_id': 'BC-7122',
  'status': 'shipped',
  'total_cents': 5250,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-04T12:36:00Z'},
 {'order_id': 'BC-7123',
  'status': 'cancelled',
  'total_cents': 1875,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-05T13:36:00Z'}]


class Client:
    """Talk to a Big Cartel store without leaving the process."""

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
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
