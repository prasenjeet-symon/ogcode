"""Operational runbooks as ordered, checkable steps.

A runbook is a tuple of ``(title, owner, minutes)`` triples. Nothing here
touches a server: the helpers only turn a runbook into the checklist an
on-call engineer works down and the estimate a status page quotes.
"""

from shop.errors import NotFoundError

__all__ = ["RUNBOOKS", "checklist", "next_step", "steps_for", "total_minutes"]

RUNBOOKS = {
    "deploy": (
        ("Freeze the release branch", "release-captain", 5),
        ("Run the smoke suite against staging", "on-call", 10),
        ("Drain traffic from the old pool", "platform", 3),
        ("Promote the new pool and watch error rate", "on-call", 15),
        ("Unfreeze the branch and announce the version", "release-captain", 2),
    ),
    "rollback": (
        ("Declare the rollback in the incident channel", "on-call", 2),
        ("Point the load balancer back at the previous pool", "platform", 5),
        ("Replay orders captured during the bad window", "data", 20),
        ("Confirm the checkout probe is green", "on-call", 5),
    ),
    "incident": (
        ("Open the incident and assign a commander", "on-call", 3),
        ("Post the first customer-facing status", "comms", 5),
        ("Identify the failing dependency", "on-call", 15),
        ("Apply the mitigation and watch for ten minutes", "platform", 15),
        ("Write the timeline before standing down", "commander", 25),
    ),
    "key-rotation": (
        ("Create the new signing key", "security", 5),
        ("Publish it to the gateway allow-list", "platform", 5),
        ("Switch the issuer and re-sign webhooks", "security", 10),
        ("Retire the old key after the grace window", "security", 2),
    ),
    "db-restore": (
        ("Snapshot the current volume before touching it", "data", 10),
        ("Restore the chosen backup into a scratch instance", "data", 30),
        ("Verify the order and ledger row counts", "data", 10),
        ("Swap the connection string and restart the workers", "platform", 8),
    ),
}


def names() -> list:
    """Every runbook name, sorted."""
    return sorted(RUNBOOKS)


def steps_for(name: str) -> list:
    """The runbook's steps as dicts numbered from one."""
    if name not in RUNBOOKS:
        raise NotFoundError("no runbook named %r" % (name,))
    return [
        {"index": position, "title": title, "owner": owner, "minutes": minutes}
        for position, (title, owner, minutes) in enumerate(RUNBOOKS[name], start=1)
    ]


def next_step(name: str, done: int) -> dict:
    """The step after ``done`` completed ones, or an empty dict at the end."""
    steps = steps_for(name)
    position = int(done)
    if position < 0:
        position = 0
    if position >= len(steps):
        return {}
    return steps[position]


def total_minutes(name: str) -> int:
    """The runbook's time estimate, added up."""
    return sum(step["minutes"] for step in steps_for(name))


def checklist(name: str) -> list:
    """One printable line per step, ready for the incident channel."""
    return [
        "[ ] %d. %s (%s, %dm)" % (step["index"], step["title"], step["owner"], step["minutes"])
        for step in steps_for(name)
    ]
