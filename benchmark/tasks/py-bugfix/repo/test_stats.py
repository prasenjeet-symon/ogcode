import unittest

from stats import mean, median, mode


class MeanTests(unittest.TestCase):
    def test_average(self):
        self.assertAlmostEqual(mean([1, 2, 3, 4]), 2.5)

    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            mean([])


class MedianTests(unittest.TestCase):
    def test_odd_count(self):
        self.assertEqual(median([3, 1, 2]), 2)

    def test_even_count(self):
        self.assertEqual(median([4, 1, 3, 2]), 2.5)

    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            median([])


class ModeTests(unittest.TestCase):
    def test_single_mode(self):
        self.assertEqual(mode([1, 2, 2, 3]), 2)

    def test_tie_picks_smallest(self):
        self.assertEqual(mode([3, 3, 5, 5]), 3)

    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            mode([])


if __name__ == "__main__":
    unittest.main()
