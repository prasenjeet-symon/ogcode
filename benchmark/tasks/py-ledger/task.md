`python3 -m unittest -q test_ledger` fails. Make it pass.

The ledger package has three modules in play: `ledger/money.py`, `ledger/fees.py`, and
`ledger/accounts.py`. The statement comes out of `ledger/report.py`. The failures are
in the implementation, not in the tests, and they are not all in one file.

Do not modify the tests: the grader restores them from git before running, so any test
edit is discarded. When you are done, that command must exit successfully.
