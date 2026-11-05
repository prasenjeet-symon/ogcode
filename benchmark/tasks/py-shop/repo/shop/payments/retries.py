"""Retry policy for charge attempts.

The schedule is exponential with a cap rather than a fixed table, because the
gateway budget is shared: five attempts at most, then the payment parks in
`failed` and the customer sees the decline.
"""

BASE_DELAY_MS = 250
MAX_DELAY_MS = 8000
MAX_ATTEMPTS = 5

RETRYABLE = ("timeout", "rate_limited", "gateway_error", "connection_reset")
FINAL = ("declined", "invalid_card", "insufficient_funds", "fraud")


def should_retry(status: str, attempt: int) -> bool:
    """Whether a payment in ``status`` gets another attempt."""
    if attempt >= MAX_ATTEMPTS:
        return False
    if attempt < 1:
        return False
    return str(status or "").strip().lower() in RETRYABLE


def next_delay(attempt: int) -> int:
    """Milliseconds to wait before attempt ``attempt + 1``, capped."""
    if attempt < 1:
        return 0
    return min(BASE_DELAY_MS * (2 ** (attempt - 1)), MAX_DELAY_MS)


def plan(status: str, attempt: int) -> dict:
    """What the caller should do next, as a dict the loop can log."""
    retry = should_retry(status, attempt)
    return {
        "retry": retry,
        "attempt": attempt,
        "delay_ms": next_delay(attempt) if retry else 0,
        "final": str(status or "").strip().lower() in FINAL,
        "reason": "retryable" if retry else "stop",
    }


def schedule(attempts: int) -> list:
    """The whole delay ladder, for tests and for the ops runbook."""
    return [next_delay(n) for n in range(1, max(int(attempts), 0) + 1)]
