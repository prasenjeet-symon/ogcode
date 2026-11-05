"""Free-text customer notes, with inline ``#tags`` pulled out of the body."""

from shop.errors import ValidationError

__all__ = ["add", "latest", "search", "tags"]

MAX_BODY = 500


def tags(body: str) -> list:
    """Every ``#tag`` in ``body``, lower-cased, without the hash."""
    found = []
    for word in str(body or "").split():
        if word.startswith("#") and len(word) > 1:
            found.append(word[1:].strip(".,;:!?").lower())
    return sorted(set(tag for tag in found if tag))


def add(notes: list, body: str, author: str = "agent") -> list:
    """Append one note and hand back the new list."""
    text = str(body or "").strip()
    if not text:
        raise ValidationError("a note needs a body")
    if len(text) > MAX_BODY:
        raise ValidationError("a note may not exceed %d characters" % MAX_BODY)
    row = {"seq": len(notes or []) + 1, "author": str(author), "body": text, "tags": tags(text)}
    return [dict(note) for note in notes or []] + [row]


def latest(notes: list, limit: int = 3) -> list:
    """The most recent ``limit`` notes, newest first."""
    kept = [dict(note) for note in notes or []]
    kept.sort(key=lambda note: int(note.get("seq") or 0), reverse=True)
    return kept[: max(0, int(limit))]


def search(notes: list, term: str) -> list:
    """Notes whose body or tags mention ``term``, case-insensitively."""
    needle = str(term or "").strip().lower()
    if not needle:
        raise ValidationError("search needs a term")
    hits = []
    for note in notes or []:
        haystack = str(note.get("body") or "").lower()
        if needle in haystack or needle in (note.get("tags") or []):
            hits.append(dict(note))
    return hits
