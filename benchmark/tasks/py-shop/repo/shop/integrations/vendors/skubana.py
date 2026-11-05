"""Skubana order sync.

Orders leave Skubana in its own vocabulary -- received, allocated, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "skubana"
LABEL = "Skubana"
AUTH_HEADER = "X-Skubana-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 1250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling', 'shipped')
STATUS_ALIASES = {'received': 'new',
 'allocated': 'fulfilling',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'S-7360',
  'status': 'received',
  'total_cents': 3000,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-02T14:48:00Z'},
 {'order_id': 'S-7361',
  'status': 'allocated',
  'total_cents': 4625,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-03T15:48:00Z'},
 {'order_id': 'S-7362',
  'status': 'shipped',
  'total_cents': 6250,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-04T16:48:00Z'},
 {'order_id': 'S-7363',
  'status': 'cancelled',
  'total_cents': 2875,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-05T17:48:00Z'}]


class Client:
    """Talk to a Skubana store without leaving the process."""

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
