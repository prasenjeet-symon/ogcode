"""Small statistics helpers used by the nightly reporting job.

Contract (the tests are the source of truth):

* ``mean([])`` raises ``ValueError``, otherwise the arithmetic mean.
* ``median([])`` raises ``ValueError``; for an even count it is the average
  of the two middle values.
* ``mode([])`` raises ``ValueError``; on a tie the smallest value wins.
"""


def mean(values):
    return sum(values) / len(values)


def median(values):
    ordered = sorted(values)
    mid = len(ordered) // 2
    return ordered[mid]


def mode(values):
    counts = {}
    for value in values:
        counts[value] = counts.get(value, 0) + 1
    return max(counts, key=counts.get)
