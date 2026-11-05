"""Reference fix: proves the py-bugfix task is solvable. Never copied into a run."""


def mean(values):
    if not values:
        raise ValueError("mean of an empty sequence")
    return sum(values) / len(values)


def median(values):
    if not values:
        raise ValueError("median of an empty sequence")
    ordered = sorted(values)
    n = len(ordered)
    if n % 2:
        return ordered[n // 2]
    return (ordered[n // 2 - 1] + ordered[n // 2]) / 2


def mode(values):
    if not values:
        raise ValueError("mode of an empty sequence")
    counts = {}
    for value in values:
        counts[value] = counts.get(value, 0) + 1
    best = max(counts.values())
    return min(value for value, count in counts.items() if count == best)
