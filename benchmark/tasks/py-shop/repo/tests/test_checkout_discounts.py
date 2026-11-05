"""Checkout pricing end to end: tiers, coupons, tax and per-line allocation."""

import unittest

from shop.catalog.models import Product
from shop.discounts.coupons import lookup
from shop.errors import NotFoundError
from shop.orders.cart import add_item
from shop.orders.checkout import checkout
from shop.orders.models import Cart


def build_cart(*specs) -> Cart:
    """Cart from ``(sku, unit price in cents, quantity)`` triples."""
    cart = Cart()
    for sku, price_cents, quantity in specs:
        product = Product(sku=sku, name=sku, category="test", price_cents=price_cents)
        add_item(cart, product, quantity)
    return cart


class TierBoundaryTest(unittest.TestCase):
    def test_subtotal_at_the_boundary_earns_the_tier(self):
        # $100.00 exactly: silver starts here, it does not start above here.
        cart = build_cart(("SKU-A", 2500, 2), ("SKU-B", 1250, 4))
        self.assertEqual(cart.subtotal_cents, 10000)

        order = checkout(cart)

        self.assertEqual(order.tier, "silver")
        self.assertEqual(order.discount_cents, 300)
        self.assertEqual(order.total_cents, 9700)


class DiscountAllocationTest(unittest.TestCase):
    def test_allocation_keeps_every_cent(self):
        cart = build_cart(("SKU-A", 2599, 1), ("SKU-B", 1201, 1), ("SKU-C", 1999, 1))
        self.assertEqual(cart.subtotal_cents, 5799)

        order = checkout(cart, coupon_code="SAVE5", tax_rate_bps=825)

        self.assertEqual(order.discount_cents, 500)
        self.assertEqual([line.discount_cents for line in order.lines], [224, 104, 172])
        self.assertEqual(
            sum(line.discount_cents for line in order.lines),
            order.discount_cents,
        )
        self.assertEqual(order.tax_cents, 437)
        self.assertEqual(order.total_cents, 5736)


class TaxBaseTest(unittest.TestCase):
    def test_tax_is_charged_on_the_discounted_subtotal(self):
        cart = build_cart(("SKU-A", 2599, 1), ("SKU-B", 401, 1))
        self.assertEqual(cart.subtotal_cents, 3000)

        order = checkout(cart, coupon_code="SAVE5", tax_rate_bps=1000)

        # 8.25% is not involved here: this cart is taxed at 10% of $25.00.
        self.assertEqual(order.tax_cents, 250)
        self.assertEqual(order.shipping_cents, 499)
        self.assertEqual(order.total_cents, 3249)


class StackingTest(unittest.TestCase):
    def test_percent_coupon_and_tier_compound_in_order(self):
        cart = build_cart(("SKU-A", 2500, 2), ("SKU-B", 1250, 4))

        order = checkout(cart, coupon_code="WELCOME10", tax_rate_bps=825)

        self.assertEqual([r["kind"] for r in order.discounts], ["coupon", "tier"])
        self.assertEqual([r["amount_cents"] for r in order.discounts], [1000, 270])
        self.assertEqual(order.discount_cents, 1270)
        self.assertEqual(order.tax_cents, 720)
        self.assertEqual(order.total_cents, 9450)
        self.assertEqual([line.discount_cents for line in order.lines], [635, 635])

    def test_shipping_is_flat_below_the_threshold(self):
        cart = build_cart(("SKU-A", 1200, 2))

        order = checkout(cart)

        self.assertEqual(order.shipping_cents, 499)
        self.assertEqual(order.total_cents, 2899)


class CouponLookupTest(unittest.TestCase):
    def test_codes_are_case_insensitive(self):
        self.assertEqual(lookup(" welcome10 ").kind, "percent")
        self.assertEqual(lookup("SAVE5").value, 500)

    def test_unknown_code_is_rejected(self):
        with self.assertRaises(NotFoundError):
            lookup("NOT-A-COUPON")


if __name__ == "__main__":
    unittest.main()
