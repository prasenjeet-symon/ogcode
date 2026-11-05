"""Money conversion and formatting."""

import unittest
from decimal import Decimal

from shop.errors import ValidationError
from shop.money import format_cents, mul_rate, to_cents


class ToCentsTest(unittest.TestCase):
    def test_string_and_float_agree(self):
        self.assertEqual(to_cents("12.34"), 1234)
        self.assertEqual(to_cents(12.34), 1234)
        self.assertEqual(to_cents(Decimal("12.34")), 1234)

    def test_whole_dollars(self):
        self.assertEqual(to_cents(7), 700)
        self.assertEqual(to_cents("7"), 700)
        self.assertEqual(to_cents("7.00"), 700)

    def test_rounds_half_up_at_the_cent(self):
        self.assertEqual(to_cents("1.005"), 101)
        self.assertEqual(to_cents("1.004"), 100)

    def test_rejects_junk(self):
        with self.assertRaises(ValidationError):
            to_cents("not money")
        with self.assertRaises(ValidationError):
            to_cents(float("nan"))


class FormatCentsTest(unittest.TestCase):
    def test_pads_the_cents(self):
        self.assertEqual(format_cents(1234), "12.34")
        self.assertEqual(format_cents(1200), "12.00")
        self.assertEqual(format_cents(7), "0.07")
        self.assertEqual(format_cents(0), "0.00")

    def test_negative_amounts(self):
        self.assertEqual(format_cents(-2599), "-25.99")
        self.assertEqual(format_cents(-5), "-0.05")


class MulRateTest(unittest.TestCase):
    def test_percentages(self):
        self.assertEqual(mul_rate(10000, 0), 0)
        self.assertEqual(mul_rate(10000, 1000), 1000)
        self.assertEqual(mul_rate(10800, 825), 891)

    def test_half_up(self):
        self.assertEqual(mul_rate(2500, 1000), 250)
        self.assertEqual(mul_rate(5299, 825), 437)


if __name__ == "__main__":
    unittest.main()
