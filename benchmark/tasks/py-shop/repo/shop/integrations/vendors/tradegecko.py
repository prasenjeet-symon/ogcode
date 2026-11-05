"""TradeGecko order sync.

Orders leave TradeGecko in its own vocabulary -- draft, active, fulfilled, void -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "tradegecko"
LABEL = "TradeGecko"
AUTH_HEADER = "X-TradeGecko-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 1000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'draft': 'new', 'active': 'paid', 'fulfilled': 'delivered', 'void': 'cancelled'}

SEED = [{'order_id': 'T-7350',
  'status': 'void',
  'total_cents': 2125,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-02T13:45:00Z'},
 {'order_id': 'T-7351',
  'status': 'draft',
  'total_cents': 3750,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-03T14:45:00Z'},
 {'order_id': 'T-7352',
  'status': 'active',
  'total_cents': 5375,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-04T15:45:00Z'},
 {'order_id': 'T-7353',
  'status': 'fulfilled',
  'total_cents': 2000,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-05T16:45:00Z'}]


class Client:
    """Talk to a TradeGecko store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Only orders worth syncing, above the vendor minimum."""
        return [dict(row) for row in self._orders if int(row["total_cents"]) >= MIN_TOTAL_CENTS]

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
