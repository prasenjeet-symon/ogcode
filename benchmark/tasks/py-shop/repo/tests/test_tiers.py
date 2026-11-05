"""Spend tiers and the discount they carry."""

import unittest

from shop.discounts.tiers import (
    tier_discount_cents,
    tier_for_subtotal,
    tier_rate_bps,
    tier_row,
)


class TierLookupTest(unittest.TestCase):
    def test_names_inside_each_band(self):
        self.assertEqual(tier_for_subtotal(0), "bronze")
        self.assertEqual(tier_for_subtotal(4999), "bronze")
        self.assertEqual(tier_for_subtotal(12000), "silver")
        self.assertEqual(tier_for_subtotal(30000), "gold")
        self.assertEqual(tier_for_subtotal(75000), "platinum")

    def test_rates_inside_each_band(self):
        self.assertEqual(tier_rate_bps(12000), 300)
        self.assertEqual(tier_rate_bps(30000), 500)
        self.assertEqual(tier_rate_bps(75000), 800)

    def test_row_pairs_a_name_with_its_rate(self):
        self.assertEqual(tier_row(30000), ("gold", 500))

    def test_discount_is_the_rate_applied_half_up(self):
        self.assertEqual(tier_discount_cents(12000), 360)
        self.assertEqual(tier_discount_cents(30000), 1500)
        self.assertEqual(tier_discount_cents(75000), 6000)

    def test_bronze_takes_nothing_off(self):
        self.assertEqual(tier_discount_cents(0), 0)
        self.assertEqual(tier_discount_cents(4999), 0)


if __name__ == "__main__":
    unittest.main()
