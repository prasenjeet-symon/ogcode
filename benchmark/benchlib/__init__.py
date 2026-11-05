"""The ogcode benchmark package.

Public entry points are the two scripts at the package root:
``run_benchmark.py`` and ``report.py``.  The modules here hold the reusable
pieces so the scripts stay readable and the tests can import them directly.
"""

from __future__ import annotations

import pathlib

VERSION = (pathlib.Path(__file__).resolve().parent.parent / "VERSION").read_text().strip()
__version__ = VERSION
