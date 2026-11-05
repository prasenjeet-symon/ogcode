"""Tiered fee schedule. Each tier takes everything up to and including its ceiling."""

from .money import percent_of


class FeeSchedule:
    def __init__(self, tiers):
        if not tiers:
            raise ValueError("a schedule needs at least one tier")
        self.tiers = sorted(tiers, key=lambda t: (t["up_to"] is None, t["up_to"] or 0))
        if self.tiers[-1]["up_to"] is not None:
            raise ValueError("the last tier must be open ended (up_to: None)")

    def fee_for(self, cents):
        """The fee a single posting of `cents` attracts."""
        if cents < 0:
            raise ValueError("a fee is computed on a non-negative amount")
        for tier in self.tiers:
            ceiling = tier["up_to"]
            if ceiling is None or cents <= ceiling:
                return percent_of(cents, tier["permille"]) + tier["flat"]
        raise AssertionError("unreachable: the last tier is open ended")
