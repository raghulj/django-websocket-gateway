"""Tests for the logout-revocation signal handler and ``force_logout_user``."""

from __future__ import annotations

from typing import Any
from unittest.mock import MagicMock


def test_logout_signal_publishes_session_revoke(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """Firing ``user_logged_out`` with a session_key publishes on _session:{key}."""
    from websocket_gateway import revocation

    captured: list[tuple[str, dict]] = []

    def fake_publish(channel: str, payload: dict) -> int:
        captured.append((channel, payload))
        return 1

    monkeypatch.setattr(revocation, "publish", fake_publish)

    request = MagicMock()
    request.session.session_key = "abc123"
    revocation._revoke_on_logout(sender=None, request=request, user=MagicMock())

    assert captured == [("_session:abc123", {"type": "revoke", "reason": "logout"})]


def test_logout_signal_with_no_request_does_nothing(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """A None request must not raise and must not publish."""
    from websocket_gateway import revocation

    called = False

    def fake_publish(channel: str, payload: dict) -> int:
        nonlocal called
        called = True
        return 0

    monkeypatch.setattr(revocation, "publish", fake_publish)
    revocation._revoke_on_logout(sender=None, request=None, user=MagicMock())
    assert called is False


def test_logout_signal_with_no_session_key_does_nothing(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """A session without session_key (anonymous logout) must not publish."""
    from websocket_gateway import revocation

    called = False

    def fake_publish(channel: str, payload: dict) -> int:
        nonlocal called
        called = True
        return 0

    monkeypatch.setattr(revocation, "publish", fake_publish)

    request = MagicMock()
    request.session.session_key = None
    revocation._revoke_on_logout(sender=None, request=request, user=MagicMock())
    assert called is False


def test_force_logout_user_publishes_user_revoke(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """force_logout_user publishes on _user:{pk}:revoke with the right payload."""
    from websocket_gateway import revocation

    captured: list[tuple[str, dict]] = []

    def fake_publish(channel: str, payload: dict) -> int:
        captured.append((channel, payload))
        return 1

    monkeypatch.setattr(revocation, "publish", fake_publish)

    user = MagicMock()
    user.pk = 42
    revocation.force_logout_user(user)

    assert captured == [("_user:42:revoke", {"type": "revoke", "reason": "force_logout"})]


def test_connect_signals_is_idempotent() -> None:
    """Calling connect_signals twice registers exactly one receiver (dispatch_uid)."""
    from django.contrib.auth.signals import user_logged_out

    from websocket_gateway import revocation

    user_logged_out.disconnect(dispatch_uid="websocket_gateway.revoke_on_logout")

    revocation.connect_signals()
    revocation.connect_signals()

    matches = [
        r for r in user_logged_out.receivers if r[0][0] == "websocket_gateway.revoke_on_logout"
    ]
    assert len(matches) == 1, f"expected exactly one receiver, found {len(matches)}"
