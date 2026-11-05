"""An append-only audit trail for the ops console.

Entries are plain dicts, so the same shape lands in the database, in the
NDJSON export and in the console table without a translation layer. Every
entry must name who did what to which target; a blank field is refused
rather than stored as an anonymous row.
"""

from shop.errors import ValidationError

__all__ = ["FIELDS", "record", "summarise", "trail"]

FIELDS = ("actor", "action", "target")


def record(actor: str, action: str, target: str, at: str) -> dict:
    """One audit entry, refusing a blank field or timestamp."""
    entry = {"actor": str(actor).strip(), "action": str(action).strip(), "target": str(target).strip()}
    blank = [key for key in FIELDS if not entry[key]]
    if blank:
        raise ValidationError("audit entry needs %s" % (", ".join(blank),))
    if not str(at).strip():
        raise ValidationError("audit entry needs a timestamp")
    entry["at"] = str(at).strip()
    return entry


def trail(entries, actor=None, action=None) -> list:
    """The entries matching both filters, in the order they arrived."""
    kept = []
    for entry in entries:
        if actor is not None and entry.get("actor") != actor:
            continue
        if action is not None and entry.get("action") != action:
            continue
        kept.append(dict(entry))
    return kept


def summarise(entries) -> dict:
    """Totals by actor and by action, each sorted alphabetically."""
    by_actor = {}
    by_action = {}
    total = 0
    for entry in entries:
        total += 1
        actor = str(entry.get("actor", ""))
        action = str(entry.get("action", ""))
        by_actor[actor] = by_actor.get(actor, 0) + 1
        by_action[action] = by_action.get(action, 0) + 1
    return {"total": total, "by_actor": dict(sorted(by_actor.items())), "by_action": dict(sorted(by_action.items()))}
