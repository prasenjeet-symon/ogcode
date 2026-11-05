import unittest

from duration import parse_duration


class ParseDurationTests(unittest.TestCase):
    def test_single_units(self):
        self.assertEqual(parse_duration("45s"), 45)
        self.assertEqual(parse_duration("90m"), 5400)
        self.assertEqual(parse_duration("2h"), 7200)
        self.assertEqual(parse_duration("3d"), 259200)

    def test_compound_segments(self):
        self.assertEqual(parse_duration("1h30m"), 5400)
        self.assertEqual(parse_duration("1d2h3m4s"), 93784)

    def test_repeated_unit_adds(self):
        self.assertEqual(parse_duration("1h1h"), 7200)

    def test_invalid_input(self):
        for bad in ("", "  ", "10", "10x", "h10", "-5m", "1h "):
            with self.assertRaises(ValueError, msg=repr(bad)):
                parse_duration(bad)


if __name__ == "__main__":
    unittest.main()
