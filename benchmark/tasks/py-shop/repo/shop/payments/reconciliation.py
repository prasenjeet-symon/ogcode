"""Match the gateway settlement file against our own ledger.

The two sides are keyed by the gateway reference, because that is the only
field both systems agree on. A row present on one side only is a finding, not
an error: the runbook says who owns each bucket.
"""


def index_by_reference(rows: list) -> dict:
    """Reference -> row. A duplicate reference keeps the last row seen."""
    index = {}
    for row in rows or []:
        reference = str(row.get("reference") or "")
        if reference:
            index[reference] = row
    return index


def reconcile(entries: list, rows: list) -> dict:
    """Diff our entries against a settlement file.

    Returns four buckets plus a one-line summary the runbook quotes.
    """
    ours = index_by_reference(entries)
    theirs = index_by_reference(rows)
    matched, mismatch = [], []
    for reference in sorted(set(ours) & set(theirs)):
        left = int(ours[reference].get("cents") or 0)
        right = int(theirs[reference].get("cents") or 0)
        if left == right:
            matched.append(reference)
        else:
            mismatch.append({"reference": reference, "ours": left, "theirs": right})
    return {
        "matched": matched,
        "amount_mismatch": mismatch,
        "missing_in_gateway": sorted(set(ours) - set(theirs)),
        "missing_in_ledger": sorted(set(theirs) - set(ours)),
        "summary": "%d matched, %d mismatched, %d one-sided"
        % (len(matched), len(mismatch), len(set(ours) ^ set(theirs))),
    }


def needs_attention(report: dict) -> bool:
    """True when a human has to look at the settlement run."""
    return bool(report.get("amount_mismatch") or report.get("missing_in_gateway") or report.get("missing_in_ledger"))
