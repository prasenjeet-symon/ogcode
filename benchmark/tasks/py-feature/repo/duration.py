"""Parse human duration strings.

Implement ``parse_duration(text) -> int`` returning whole seconds.

Grammar: one or more ``<number><unit>`` segments, in any order, with no
separators, e.g. ``"1h30m"``, ``"45s"``, ``"2d"``, ``"1d2h3m4s"``.
Units: ``d`` (86400), ``h`` (3600), ``m`` (60), ``s`` (1).

Rules:

* the result is the sum of every segment
* a repeated unit adds again (``"1h1h"`` is 7200)
* empty input, whitespace anywhere, an unknown unit, a missing number or a
  negative number raises ``ValueError``
* a trailing remainder that does not form a whole segment raises ``ValueError``
"""


def parse_duration(text):
    raise NotImplementedError("parse_duration is not implemented yet")
