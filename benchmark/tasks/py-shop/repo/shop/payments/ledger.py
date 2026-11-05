"""A double-entry payment ledger.

Every movement is posted once, with a debit account and a credit account, and
balances are derived rather than stored. The class deliberately has no
persistence: the server wraps it around the payments table.
"""

from shop.errors import ValidationError

ACCOUNTS = ("cash", "gateway_clearing", "revenue", "fees", "refunds", "chargebacks")


class Ledger:
    """An append-only list of postings between known accounts."""

    def __init__(self, accounts=ACCOUNTS):
        self.accounts = tuple(accounts)
        self._entries = []

    def post(self, debit: str, credit: str, cents: int, reference: str = "") -> dict:
        """Move ``cents`` from ``debit`` to ``credit`` and return the entry."""
        for account in (debit, credit):
            if account not in self.accounts:
                raise ValidationError("unknown account %r" % (account,))
        if debit == credit:
            raise ValidationError("a posting needs two different accounts")
        amount = int(cents)
        if amount <= 0:
            raise ValidationError("a posting is a positive number of cents")
        entry = {"seq": len(self._entries) + 1, "debit": debit, "credit": credit, "cents": amount, "reference": reference}
        self._entries.append(entry)
        return dict(entry)

    def entries(self, account: str = None) -> list:
        """Every posting, or only those touching ``account``."""
        if account is None:
            return [dict(entry) for entry in self._entries]
        return [dict(entry) for entry in self._entries if account in (entry["debit"], entry["credit"])]

    def balance(self, account: str) -> int:
        """Net cents held by ``account``: credits in, debits out."""
        if account not in self.accounts:
            raise ValidationError("unknown account %r" % (account,))
        total = 0
        for entry in self._entries:
            if entry["credit"] == account:
                total += entry["cents"]
            if entry["debit"] == account:
                total -= entry["cents"]
        return total

    def is_balanced(self) -> bool:
        """True when every account nets to zero across the whole book."""
        return all(self.balance(account) == 0 for account in self.accounts)

    def total_posted(self) -> int:
        """Gross cents moved, which is what the finance export counts."""
        return sum(entry["cents"] for entry in self._entries)
