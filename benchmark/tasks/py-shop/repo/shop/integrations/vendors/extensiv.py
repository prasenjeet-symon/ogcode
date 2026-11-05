"""Extensiv order sync.

Orders leave Extensiv in its own vocabulary -- draft, approved, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "extensiv"
LABEL = "Extensiv"
AUTH_HEADER = "X-Extensiv-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 1500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'draft': 'new', 'approved': 'paid', 'shipped': 'shipped', 'cancelled': 'cancelled'}

SEED = [{'order_id': 'E-7370',
  'status': 'approved',
  'total_cents': 3875,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-02T15:51:00Z'},
 {'order_id': 'E-7371',
  'status': 'shipped',
  'total_cents': 5500,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-03T16:51:00Z'},
 {'order_id': 'E-7372',
  'status': 'cancelled',
  'total_cents': 2125,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-04T17:51:00Z'},
 {'order_id': 'E-7373',
  'status': 'draft',
  'total_cents': 3750,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-05T08:51:00Z'}]


class Client:
    """Talk to a Extensiv store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Every row, with the unit count lifted out of the vendor shape."""
        out = []
        for row in self._orders:
            record = dict(row)
            record["unit_count"] = int(record.pop("units", 0))
            out.append(record)
        return out

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
