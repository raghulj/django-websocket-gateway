"""Tests for ``websocket_gateway.views.ws_auth``."""

from __future__ import annotations

import json
from typing import Any

import pytest
from django.contrib.auth import get_user_model
from django.contrib.sessions.backends.db import SessionStore
from django.test import Client

pytestmark = pytest.mark.django_db


@pytest.fixture
def auth_headers(apply_settings: dict) -> dict[str, str]:
    """Headers carrying the valid X-Internal-Auth secret."""
    return {"HTTP_X_INTERNAL_AUTH": apply_settings["INTERNAL_SECRET"]}


@pytest.fixture
def active_user(db: Any) -> Any:
    """A persisted, active Django user."""
    User = get_user_model()
    return User.objects.create_user(username="alice", password="pw")


@pytest.fixture
def session_for(active_user: Any) -> Any:
    """A SessionStore already populated with ``_auth_user_id`` for ``active_user``."""

    def _make(user: Any | None = None) -> str:
        store = SessionStore()
        store["_auth_user_id"] = str((user or active_user).pk)
        store.save()
        return store.session_key

    return _make


def test_get_returns_405(auth_headers: dict, client: Client) -> None:
    """The endpoint is POST-only."""
    response = client.get("/internal/ws-auth/", **auth_headers)
    assert response.status_code == 405


def test_missing_session_header_returns_401(auth_headers: dict, client: Client) -> None:
    """Absent X-Forwarded-Session → 401 + {"authenticated": false}."""
    response = client.post("/internal/ws-auth/", **auth_headers)
    assert response.status_code == 401
    assert response.json() == {"authenticated": False}


def test_invalid_session_returns_401(auth_headers: dict, client: Client) -> None:
    """A bogus session_key → 401."""
    response = client.post(
        "/internal/ws-auth/", HTTP_X_FORWARDED_SESSION="not-a-real-session", **auth_headers
    )
    assert response.status_code == 401


def test_inactive_user_returns_401(
    auth_headers: dict, client: Client, active_user: Any, session_for: Any
) -> None:
    """A session belonging to an inactive user → 401."""
    session_key = session_for()
    active_user.is_active = False
    active_user.save()

    response = client.post(
        "/internal/ws-auth/", HTTP_X_FORWARDED_SESSION=session_key, **auth_headers
    )
    assert response.status_code == 401


def test_valid_session_returns_200_with_allowed_channels(
    auth_headers: dict, client: Client, active_user: Any, session_for: Any
) -> None:
    """A valid, active session → 200 + user_id + username + allowed_channels."""
    session_key = session_for()

    response = client.post(
        "/internal/ws-auth/", HTTP_X_FORWARDED_SESSION=session_key, **auth_headers
    )
    assert response.status_code == 200
    body = response.json()
    assert body["authenticated"] is True
    assert body["user_id"] == active_user.pk
    assert body["username"] == "alice"
    assert body["allowed_channels"] == [f"user-{active_user.pk}"]


def test_callback_returning_non_list_raises_type_error(
    auth_headers: dict,
    apply_settings: dict,
    client: Client,
    active_user: Any,
    session_for: Any,
) -> None:
    """A callback violating its contract raises TypeError (→ 500 to caller)."""
    apply_settings["AUTHORIZATION_CALLBACK"] = "websocket_gateway.tests._callbacks.returns_non_list"
    session_key = session_for()

    with pytest.raises(TypeError, match="list\\[str\\]"):
        client.post(
            "/internal/ws-auth/",
            HTTP_X_FORWARDED_SESSION=session_key,
            **auth_headers,
        )


def test_csrf_exempt(auth_headers: dict, active_user: Any, session_for: Any) -> None:
    """The endpoint must be reachable without CSRF protection (server-to-server)."""
    enforced = Client(enforce_csrf_checks=True)
    session_key = session_for()
    response = enforced.post(
        "/internal/ws-auth/",
        HTTP_X_FORWARDED_SESSION=session_key,
        **auth_headers,
    )
    assert response.status_code == 200


def test_internal_auth_header_required(
    apply_settings: dict,
    client: Client,
    active_user: Any,
    session_for: Any,
) -> None:
    """Without X-Internal-Auth, even a valid session is 403."""
    session_key = session_for()
    response = client.post("/internal/ws-auth/", HTTP_X_FORWARDED_SESSION=session_key)
    assert response.status_code == 403


def test_response_does_not_leak_secret(
    auth_headers: dict, client: Client, active_user: Any, session_for: Any, apply_settings: dict
) -> None:
    """The 200 response body must not contain the secret value."""
    session_key = session_for()
    response = client.post(
        "/internal/ws-auth/", HTTP_X_FORWARDED_SESSION=session_key, **auth_headers
    )
    assert apply_settings["INTERNAL_SECRET"] not in response.content.decode()
    json.loads(response.content)  # body is valid JSON
