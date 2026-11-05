"""Lines that will strain the warehouse.

Any line of three units or more is worth a pick-face check, because three
is where the site's case handling starts to matter."""

__all__ = ["build"]
PRESSURE_QTY = 3


def build(orders: list[dict], **kwargs) -> dict:
    """Large lines grouped by SKU, heaviest first."""
    limit = int(kwargs.get("limit", 10))
    rows = {}
    for order in orders or []:
        for row in order.get("lines") or []:
            qty = int(row.get("qty") or 0)
            if qty < PRESSURE_QTY:
                continue
            sku = str(row.get("sku") or "")
            entry = rows.setdefault(sku, {"sku": sku, "lines": 0, "units": 0, "weight_g": 0})
            entry["lines"] += 1
            entry["units"] += qty
            entry["weight_g"] += qty * int(row.get("weight_g") or 0)
    ordered = [rows[key] for key in sorted(rows)]
    ordered.sort(key=lambda row: (-row["units"], row["sku"]))
    return {"report": "inventory_pressure", "rows": ordered[: max(0, limit)], "skus": len(ordered)}
