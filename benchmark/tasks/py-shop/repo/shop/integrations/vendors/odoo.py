"""Odoo order sync.

Orders leave Odoo in its own vocabulary -- draft, sale, done, cancel -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "odoo"
LABEL = "Odoo"
AUTH_HEADER = "X-Odoo-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 1500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'draft': 'new', 'sale': 'paid', 'done': 'delivered', 'cancel': 'cancelled'}

SEED = [{'order_id': 'O-7300',
  'status': 'done',
  'total_cents': 2750,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-02T08:30:00Z'},
 {'order_id': 'O-7301',
  'status': 'cancel',
  'total_cents': 4375,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-03T09:30:00Z'},
 {'order_id': 'O-7302',
  'status': 'draft',
  'total_cents': 6000,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-04T10:30:00Z'},
 {'order_id': 'O-7303',
  'status': 'sale',
  'total_cents': 2625,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-05T11:30:00Z'}]


class Client:
    """Talk to a Odoo store without leaving the process."""

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
