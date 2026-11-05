"""Merchandise revenue by product category.

Category is read off the order line rather than looked up in the catalogue,
which keeps the report usable for orders whose SKU has since been retired."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Line revenue per category, richest category first."""
    rows = {}
    for order in orders or []:
        for row in order.get("lines") or []:
            category = str(row.get("category") or "uncategorised")
            entry = rows.setdefault(category, {"category": category, "units": 0, "cents": 0})
            qty = int(row.get("qty") or 0)
            entry["units"] += qty
            entry["cents"] += qty * int(row.get("unit_cents") or 0)
    ordered = [rows[key] for key in sorted(rows)]
    ordered.sort(key=lambda row: (-row["cents"], row["category"]))
    return {"report": "revenue_by_category", "rows": ordered, "categories": len(ordered)}
