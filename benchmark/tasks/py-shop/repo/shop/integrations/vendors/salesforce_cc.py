"""Salesforce CC order sync.

Orders leave Salesforce CC in its own vocabulary -- created, exported, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "salesforce_cc"
LABEL = "Salesforce CC"
AUTH_HEADER = "X-Salesforce CC-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 2500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'created': 'new', 'exported': 'paid', 'shipped': 'shipped', 'cancelled': 'cancelled'}

SEED = [{'order_id': 'SC-7060',
  'status': 'shipped',
  'total_cents': 1750,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-02T14:18:00Z'},
 {'order_id': 'SC-7061',
  'status': 'cancelled',
  'total_cents': 3375,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-03T15:18:00Z'},
 {'order_id': 'SC-7062',
  'status': 'created',
  'total_cents': 5000,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-04T16:18:00Z'},
 {'order_id': 'SC-7063',
  'status': 'exported',
  'total_cents': 1625,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-05T17:18:00Z'}]


class Client:
    """Talk to a Salesforce CC store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Read through the id index, ordered by order id."""
        self._by_id = {row["order_id"]: dict(row) for row in self._orders}
        return [self._by_id[order_id] for order_id in sorted(self._by_id)]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
