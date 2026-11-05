"""Edit-distance helpers behind the did-you-mean suggestion.

The distance is the classic Levenshtein edit count computed with two rows
instead of a full matrix, so suggesting a correction for a 30 character
query stays cheap enough to run inside a request.
"""

__all__ = ["closest", "distance", "within"]


def distance(left: str, right: str) -> int:
    """The number of inserts, deletes and substitutions between two words."""
    first = str(left or "")
    second = str(right or "")
    if first == second:
        return 0
    if not first:
        return len(second)
    if not second:
        return len(first)
    previous = list(range(len(second) + 1))
    for row, char in enumerate(first, start=1):
        current = [row]
        for column, other in enumerate(second, start=1):
            cost = 0 if char == other else 1
            current.append(
                min(previous[column] + 1, current[column - 1] + 1, previous[column - 1] + cost)
            )
        previous = current
    return previous[-1]


def within(left: str, right: str, max_distance: int = 2) -> bool:
    """Whether two words are close enough to be the same misspelling."""
    first = str(left or "").lower()
    second = str(right or "").lower()
    if abs(len(first) - len(second)) > abs(int(max_distance)):
        return False
    return distance(first, second) <= abs(int(max_distance))


def closest(term: str, vocabulary, max_distance: int = 2, limit: int = 5) -> list:
    """Vocabulary entries near ``term``, nearest first, alphabetically on a tie."""
    word = str(term or "").strip().lower()
    if not word:
        return []
    scored = []
    for candidate in vocabulary:
        other = str(candidate).strip().lower()
        steps = distance(word, other)
        if steps <= abs(int(max_distance)):
            scored.append((steps, len(other), other))
    scored.sort()
    return [other for _steps, _length, other in scored[: max(0, int(limit))]]
