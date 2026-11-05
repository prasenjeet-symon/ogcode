"""Shopware order sync.

Orders leave Shopware in its own vocabulary -- open, in_progress, completed, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "shopware"
LABEL = "Shopware"
AUTH_HEADER = "X-Shopware-Token"
PAGE_SIZE = 40
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
STATUS_ALIASES = {'open': 'new',
 'in_progress': 'fulfilling',
 'completed': 'delivered',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'S-7080',
  'status': 'open',
  'total_cents': 3500,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-02T16:24:00Z'},
 {'order_id': 'S-7081',
  'status': 'in_progress',
  'total_cents': 5125,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-03T17:24:00Z'},
 {'order_id': 'S-7082',
  'status': 'completed',
  'total_cents': 1750,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-04T08:24:00Z'},
 {'order_id': 'S-7083',
  'status': 'cancelled',
  'total_cents': 3375,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-05T09:24:00Z'}]


class Client:
    """Talk to a Shopware store without leaving the process."""

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
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
