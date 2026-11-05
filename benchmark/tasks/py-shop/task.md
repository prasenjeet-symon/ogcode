`python3 -m unittest discover -q -s tests -t .` fails. Make it pass.

This is a live service with a lot of code in it. The failures are in the
order-pricing path: cart totals, spend tiers, coupon stacking and tax. They are
in the implementation, not in the tests, and they are not all in one file.

Do not modify the tests: the grader restores them from git before running, so
any test edit is discarded. When you are done, that command must exit
successfully.
