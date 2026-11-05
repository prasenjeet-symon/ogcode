"""Ecwid order sync.

Orders leave Ecwid in its own vocabulary -- pending, paid, shipped, cancelled, refunded -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "ecwid"
LABEL = "Ecwid"
AUTH_HEADER = "X-Ecwid-Token"
PAGE_SIZE = 40
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
STATUS_ALIASES = {'pending': 'new',
 'paid': 'paid',
 'shipped': 'shipped',
 'cancelled': 'cancelled',
 'refunded': 'refunded'}

SEED = [{'order_id': 'E-7130',
  'status': 'cancelled',
  'total_cents': 2875,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-02T11:39:00Z'},
 {'order_id': 'E-7131',
  'status': 'refunded',
  'total_cents': 4500,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-03T12:39:00Z'},
 {'order_id': 'E-7132',
  'status': 'pending',
  'total_cents': 6125,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-04T13:39:00Z'},
 {'order_id': 'E-7133',
  'status': 'paid',
  'total_cents': 2750,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-05T14:39:00Z'}]


class Client:
    """Talk to a Ecwid store without leaving the process."""

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
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
