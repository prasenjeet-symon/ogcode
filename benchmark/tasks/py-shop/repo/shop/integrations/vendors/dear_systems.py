"""DEAR Systems order sync.

Orders leave DEAR Systems in its own vocabulary -- draft, authorised, packed, voided -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "dear_systems"
LABEL = "DEAR Systems"
AUTH_HEADER = "X-DEAR Systems-Token"
PAGE_SIZE = 40
MIN_TOTAL_CENTS = 2250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'fulfilling')
STATUS_ALIASES = {'draft': 'new', 'authorised': 'paid', 'packed': 'fulfilling', 'voided': 'cancelled'}

SEED = [{'order_id': 'DS-7330',
  'status': 'authorised',
  'total_cents': 5375,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-02T11:39:00Z'},
 {'order_id': 'DS-7331',
  'status': 'packed',
  'total_cents': 2000,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-03T12:39:00Z'},
 {'order_id': 'DS-7332',
  'status': 'voided',
  'total_cents': 3625,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-04T13:39:00Z'},
 {'order_id': 'DS-7333',
  'status': 'draft',
  'total_cents': 5250,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-05T14:39:00Z'}]


class Client:
    """Talk to a DEAR Systems store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Every row, with the native status mapped to ours."""
        out = []
        for row in self._orders:
            record = dict(row)
            record["status"] = STATUS_ALIASES.get(row["status"], "unknown")
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
