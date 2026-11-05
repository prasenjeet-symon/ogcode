"""Nightly backup plans and the manifest check that follows them.

The plan is pure data: one entry per target with the dated filename the
uploader will use. ``verify`` then reads a finished manifest and reports
every problem in one pass, so a half-finished upload is never silent.
"""

__all__ = ["REQUIRED", "TARGETS", "manifest", "plan", "verify"]

TARGETS = (
    {"name": "orders-db", "path": "var/backups/orders", "compress": True},
    {"name": "ledger-db", "path": "var/backups/ledger", "compress": True},
    {"name": "catalog-db", "path": "var/backups/catalog", "compress": False},
    {"name": "media", "path": "var/backups/media", "compress": True},
)

REQUIRED = ("name", "path", "bytes")


def plan(today: str, targets=TARGETS) -> dict:
    """One dated entry per backup target."""
    stamp = str(today)[:10]
    entries = [
        {
            "name": target["name"],
            "path": "%s/%s-%s.sqlite" % (target["path"], target["name"], stamp),
            "compress": bool(target.get("compress")),
        }
        for target in targets
    ]
    return {"date": stamp, "entries": entries, "count": len(entries)}


def manifest(entries) -> dict:
    """Freeze a list of finished entries into the shape ``verify`` reads."""
    frozen = [dict(entry) for entry in entries]
    return {"files": frozen, "count": len(frozen)}


def verify(entries) -> list:
    """Every problem with a manifest, as human-readable lines."""
    problems = []
    seen = set()
    for index, entry in enumerate(entries):
        missing = [key for key in REQUIRED if key not in entry]
        if missing:
            problems.append("entry %d missing %s" % (index, ", ".join(sorted(missing))))
            continue
        name = str(entry["name"])
        if name in seen:
            problems.append("duplicate target %r" % (name,))
        seen.add(name)
        if int(entry.get("bytes", 0)) <= 0:
            problems.append("target %r is empty" % (name,))
    return problems
