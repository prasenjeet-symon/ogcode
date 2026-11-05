"""Reference fix: proves the py-feature task is solvable. Never copied into a run."""

import re

_UNITS = {"d": 86400, "h": 3600, "m": 60, "s": 1}
_SEGMENT = re.compile(r"(\d+)([dhms])")


def parse_duration(text):
    if not text or text != text.strip():
        raise ValueError("duration must be non-empty and free of whitespace")
    total = 0
    pos = 0
    while pos < len(text):
        match = _SEGMENT.match(text, pos)
        if not match:
            raise ValueError(f"invalid duration segment at {pos}: {text!r}")
        total += int(match.group(1)) * _UNITS[match.group(2)]
        pos = match.end()
    return total
