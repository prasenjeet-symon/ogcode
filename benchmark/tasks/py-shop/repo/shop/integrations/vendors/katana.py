"""Katana order sync.

Orders leave Katana in its own vocabulary -- draft, in_progress, delivered, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "katana"
LABEL = "Katana"
AUTH_HEADER = "X-Katana-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 2500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling')
STATUS_ALIASES = {'draft': 'new',
 'in_progress': 'fulfilling',
 'delivered': 'delivered',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'K-7340',
  'status': 'delivered',
  'total_cents': 6250,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-02T12:42:00Z'},
 {'order_id': 'K-7341',
  'status': 'cancelled',
  'total_cents': 2875,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-03T13:42:00Z'},
 {'order_id': 'K-7342',
  'status': 'draft',
  'total_cents': 4500,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-04T14:42:00Z'},
 {'order_id': 'K-7343',
  'status': 'in_progress',
  'total_cents': 6125,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-05T15:42:00Z'}]


class Client:
    """Talk to a Katana store without leaving the process."""

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
