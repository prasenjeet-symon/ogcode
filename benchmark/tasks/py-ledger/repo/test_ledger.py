import unittest

from ledger import accounts, fees, money, report


def schedule():
    return fees.FeeSchedule(
        [
            {"up_to": 500, "permille": 10, "flat": 25},
            {"up_to": 5000, "permille": 5, "flat": 50},
            {"up_to": None, "permille": 1, "flat": 100},
        ]
    )


class MoneyTest(unittest.TestCase):
    def test_parse_and_format(self):
        self.assertEqual(money.parse_amount("12.3"), 1230)
        self.assertEqual(money.parse_amount("-0.07"), -7)
        self.assertEqual(money.format_cents(-7), "-0.07")

    def test_a_half_cent_becomes_a_whole_cent(self):
        self.assertEqual(money.percent_of(5, 100), 1)
        self.assertEqual(money.percent_of(15, 100), 2)
        self.assertEqual(money.percent_of(25, 100), 3)

    def test_rounding_is_symmetric_around_zero(self):
        self.assertEqual(money.percent_of(2, 250), 1)


class FeeScheduleTest(unittest.TestCase):
    def test_a_ceiling_belongs_to_its_own_tier(self):
        self.assertEqual(schedule().fee_for(499), 30)
        self.assertEqual(schedule().fee_for(500), 30)

    def test_the_next_tier_starts_one_cent_later(self):
        self.assertEqual(schedule().fee_for(501), 53)

    def test_the_open_ended_tier_takes_the_rest(self):
        self.assertEqual(schedule().fee_for(10000), 110)


class StatementTest(unittest.TestCase):
    def account(self):
        acct = accounts.Account("household")
        for category, text in [
            ("Rent", "1200.00"),
            ("Utilities", "85.40"),
            ("fees", "12.00"),
            ("Rent", "0.60"),
        ]:
            acct.post(accounts.Entry(category, money.parse_amount(text)))
        return acct

    def test_balance_sums_every_posting(self):
        self.assertEqual(self.account().balance(), 129800)

    def test_categories_sort_without_regard_to_case(self):
        self.assertEqual(report.categories_in_order(self.account()), ["fees", "Rent", "Utilities"])

    def test_statement_is_one_aligned_line_per_category(self):
        self.assertEqual(
            report.statement(self.account()),
            "\n".join(
                [
                    "fees" + " " * 13 + "12.00",
                    "Rent" + " " * 11 + "1200.60",
                    "Utilities" + " " * 8 + "85.40",
                    "total" + " " * 10 + "1298.00",
                ]
            ),
        )


if __name__ == "__main__":
    unittest.main()
