"""Cin7 order sync.

Orders leave Cin7 in its own vocabulary -- draft, authorised, shipped, voided -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "cin7"
LABEL = "Cin7"
AUTH_HEADER = "X-Cin7-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 2000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'draft': 'new', 'authorised': 'paid', 'shipped': 'shipped', 'voided': 'cancelled'}

SEED = [{'order_id': 'C-7320',
  'status': 'draft',
  'total_cents': 4500,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-02T10:36:00Z'},
 {'order_id': 'C-7321',
  'status': 'authorised',
  'total_cents': 6125,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-03T11:36:00Z'},
 {'order_id': 'C-7322',
  'status': 'shipped',
  'total_cents': 2750,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-04T12:36:00Z'},
 {'order_id': 'C-7323',
  'status': 'voided',
  'total_cents': 4375,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-05T13:36:00Z'}]


class Client:
    """Talk to a Cin7 store without leaving the process."""

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
