"""An account holds entries and answers with a balance and category subtotals."""

from .money import percent_of


class Entry:
    def __init__(self, category, cents, memo=""):
        if not category:
            raise ValueError("an entry needs a category")
        self.category = category
        self.cents = int(cents)
        self.memo = memo


class Account:
    def __init__(self, name):
        self.name = name
        self.entries = []

    def post(self, entry):
        self.entries.append(entry)
        return self.balance()

    def balance(self):
        return sum(entry.cents for entry in self.entries)

    def by_category(self):
        """(category, total) pairs, alphabetically by category."""
        totals = {}
        for entry in self.entries:
            totals[entry.category] = totals.get(entry.category, 0) + entry.cents
        return sorted(totals.items())
