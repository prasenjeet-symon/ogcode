"""Erply order sync.

Orders leave Erply in its own vocabulary -- draft, confirmed, fulfilled, voided -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "erply"
LABEL = "Erply"
AUTH_HEADER = "X-Erply-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 1750
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'draft': 'new', 'confirmed': 'paid', 'fulfilled': 'delivered', 'voided': 'cancelled'}

SEED = [{'order_id': 'E-7310',
  'status': 'voided',
  'total_cents': 3625,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-02T09:33:00Z'},
 {'order_id': 'E-7311',
  'status': 'draft',
  'total_cents': 5250,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-03T10:33:00Z'},
 {'order_id': 'E-7312',
  'status': 'confirmed',
  'total_cents': 1875,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-04T11:33:00Z'},
 {'order_id': 'E-7313',
  'status': 'fulfilled',
  'total_cents': 3500,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-05T12:33:00Z'}]


class Client:
    """Talk to a Erply store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One row per order id, the last write winning."""
        latest = {}
        for row in self._orders:
            latest[row["order_id"]] = row
        return [dict(latest[order_id]) for order_id in sorted(latest)]

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
