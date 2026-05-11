"""Authorization-callback stand-ins used by the test suite.

These are imported via dotted-path strings (``AUTHORIZATION_CALLBACK`` setting)
so they need a stable, importable module location.
"""

from __future__ import annotations

from typing import Any


def allow_test_channel(user: Any) -> list[str]:
    """Return a single allowed channel scoped to the user's primary key.

    Args:
        user: The Django user instance.

    Returns:
        A list with one channel name of the form ``"user-{pk}"``.
    """
    return [f"user-{user.pk}"]


def returns_non_list(user: Any) -> str:
    """Authorization callback that violates the list[str] contract."""
    return "not a list"


NOT_CALLABLE = "i am a string, not a function"
