"""Reconcile a local stock snapshot against the remote site's counts."""

from shop.errors import ValidationError

__all__ = ["apply", "diff", "plan"]


def _clean(counts) -> dict:
    if not isinstance(counts, dict):
        raise ValidationError("stock counts must be a mapping of sku to units")
    return {str(sku): int(units) for sku, units in counts.items()}


def diff(local: dict, remote: dict) -> list:
    """Every SKU where the two snapshots disagree, ordered by SKU."""
    left = _clean(local)
    right = _clean(remote)
    rows = []
    for sku in sorted(set(left) | set(right)):
        here = left.get(sku, 0)
        there = right.get(sku, 0)
        if here == there:
            continue
        rows.append(
            {
                "sku": sku,
                "local": here,
                "remote": there,
                "delta": there - here,
                "direction": "pull" if there > here else "push",
            }
        )
    return rows


def plan(local: dict, remote: dict) -> dict:
    """Split the disagreements into the two work queues."""
    rows = diff(local, remote)
    return {
        "pushes": [row for row in rows if row["direction"] == "push"],
        "pulls": [row for row in rows if row["direction"] == "pull"],
        "skus": len(rows),
    }


def apply(local: dict, remote: dict, dry_run: bool = False) -> dict:
    """Write the remote counts into the local snapshot."""
    rows = diff(local, remote)
    if not dry_run:
        for row in rows:
            local[row["sku"]] = row["remote"]
    return {"applied": 0 if dry_run else len(rows), "pending": len(rows), "rows": rows}
