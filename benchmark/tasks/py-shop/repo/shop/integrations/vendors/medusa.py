"""Medusa order sync.

Orders leave Medusa in its own vocabulary -- pending, completed, canceled, requires_action -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "medusa"
LABEL = "Medusa"
AUTH_HEADER = "X-Medusa-Token"
PAGE_SIZE = 40
MIN_TOTAL_CENTS = 2000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'pending')
STATUS_ALIASES = {'pending': 'new',
 'completed': 'paid',
 'canceled': 'cancelled',
 'requires_action': 'pending'}

SEED = [{'order_id': 'M-7180',
  'status': 'canceled',
  'total_cents': 2250,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-02T16:54:00Z'},
 {'order_id': 'M-7181',
  'status': 'requires_action',
  'total_cents': 3875,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-03T17:54:00Z'},
 {'order_id': 'M-7182',
  'status': 'pending',
  'total_cents': 5500,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-04T08:54:00Z'},
 {'order_id': 'M-7183',
  'status': 'completed',
  'total_cents': 2125,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-05T09:54:00Z'}]


class Client:
    """Talk to a Medusa store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One sales channel only, set on the client."""
        return [dict(row) for row in self._orders if row["channel"] == self.channel]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
