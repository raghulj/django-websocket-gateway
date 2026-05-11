"""Shared pytest fixtures for the websocket_gateway test suite.

Fixtures defined here are auto-discovered by pytest. They centralise the
``WEBSOCKET_GATEWAY`` settings dict (so individual tests focus on the behaviour
under test, not on plumbing) and the fake Redis client (so no test ever talks
to a real Redis).
"""

from __future__ import annotations

import secrets
from collections.abc import Iterator
from typing import Any

import fakeredis
import pytest


@pytest.fixture
def valid_secret() -> str:
    """A 48-character secret distinct from ``settings.SECRET_KEY``.

    Generated fresh per test so any accidental cross-test leak would produce a
    mismatch failure rather than a passing test against the wrong secret.
    """
    return secrets.token_urlsafe(48)


@pytest.fixture
def valid_config(valid_secret: str) -> dict[str, Any]:
    """A complete, valid ``WEBSOCKET_GATEWAY`` settings dict.

    Tests mutate this fixture (delete keys, set invalid values) to exercise
    failure paths. Mutations stay scoped to the test because pytest delivers
    a fresh dict per test function.
    """
    return {
        "INTERNAL_SECRET": valid_secret,
        "REDIS_URL": "redis://localhost:6379/0",
        "AUTHORIZATION_CALLBACK": "websocket_gateway.tests._callbacks.allow_test_channel",
        "ALLOWED_ORIGINS": ["https://app.example.com"],
    }


@pytest.fixture
def apply_settings(
    settings,  # pytest-django settings fixture
    valid_config: dict[str, Any],
) -> Iterator[dict[str, Any]]:
    """Install ``valid_config`` as ``settings.WEBSOCKET_GATEWAY`` for the test.

    Yields the same dict so the test can mutate it before code under test reads
    it. ``pytest-django`` restores settings automatically at test teardown.
    """
    settings.WEBSOCKET_GATEWAY = valid_config
    yield valid_config


@pytest.fixture
def fake_redis() -> fakeredis.FakeStrictRedis:
    """An in-memory Redis substitute for publish tests.

    Uses ``fakeredis.FakeStrictRedis`` rather than a server connection so the
    test suite stays hermetic. The instance is fresh per test.
    """
    return fakeredis.FakeStrictRedis(decode_responses=False)


@pytest.fixture
def patched_publish_client(
    monkeypatch: pytest.MonkeyPatch,
    fake_redis: fakeredis.FakeStrictRedis,
) -> Iterator[fakeredis.FakeStrictRedis]:
    """Swap the cached publish-client with ``fake_redis``.

    Tests use this when they want to assert on ``redis.publish`` calls without
    a real Redis. The cache is reset before AND after each test to prevent
    bleeding between tests (publish.py keeps a module-global client).
    """
    import importlib

    publish_module = importlib.import_module("websocket_gateway.publish")

    monkeypatch.setattr(publish_module, "_client", fake_redis, raising=False)
    yield fake_redis
    monkeypatch.setattr(publish_module, "_client", None, raising=False)
