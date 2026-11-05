"""Incident severity and the escalation ladder.

Severity follows outage length, not the noise level of the report: a
checkout that has been refusing payments for two hours is a sev1 whatever
channel it was reported in. Escalation hands the incident to the next
responder once it has outlived its severity's window.
"""

from shop.errors import ValidationError

__all__ = ["ESCALATE_AFTER", "SERVICES", "SEVERITIES", "escalate", "open_incident", "severity_for"]

SEVERITIES = ("sev1", "sev2", "sev3", "sev4")

ESCALATE_AFTER = {"sev1": 15, "sev2": 30, "sev3": 120, "sev4": 480}

SERVICES = ("checkout", "catalog", "payments", "inventory", "search", "notifications")

LADDER = {"sev4": "sev3", "sev3": "sev2", "sev2": "sev1", "sev1": "sev1"}


def severity_for(minutes: int) -> str:
    """The severity an outage of that many minutes has earned."""
    elapsed = int(minutes)
    if elapsed >= 120:
        return "sev1"
    if elapsed >= 45:
        return "sev2"
    if elapsed >= 15:
        return "sev3"
    return "sev4"


def open_incident(title: str, service: str, minutes: int = 0, opened_at: str = "2026-02-11T09:30:00Z") -> dict:
    """A new incident dict, refusing a blank title or unknown service."""
    if not str(title).strip():
        raise ValidationError("an incident needs a title")
    if service not in SERVICES:
        raise ValidationError("unknown service %r" % (service,))
    return {
        "title": str(title).strip(),
        "service": service,
        "minutes": int(minutes),
        "opened_at": str(opened_at),
        "severity": severity_for(minutes),
        "page": 1,
    }


def escalate(incident: dict) -> dict:
    """The same incident one rung up, with its page count raised."""
    raised = dict(incident)
    current = str(raised.get("severity", "sev4"))
    raised["severity"] = LADDER.get(current, "sev1")
    raised["page"] = int(raised.get("page", 1)) + 1
    return raised
