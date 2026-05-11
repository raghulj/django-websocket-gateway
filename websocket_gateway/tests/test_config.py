"""Tests for ``websocket_gateway._config``.

Every test that involves the shared secret asserts that the secret value never
appears in the exception message. Hard rule #3 of CLAUDE.md is enforced here.
"""

from __future__ import annotations

import pytest
from django.conf import settings
from django.core.exceptions import ImproperlyConfigured


def test_missing_websocket_gateway_setting_raises() -> None:
    """get_config() raises when the WEBSOCKET_GATEWAY dict is absent."""
    from websocket_gateway._config import get_config

    if hasattr(settings, "WEBSOCKET_GATEWAY"):
        del settings.WEBSOCKET_GATEWAY

    with pytest.raises(ImproperlyConfigured, match="WEBSOCKET_GATEWAY"):
        get_config()


def test_missing_internal_secret_raises(apply_settings: dict) -> None:
    """Absent INTERNAL_SECRET is rejected with a helpful message."""
    from websocket_gateway._config import get_config

    del apply_settings["INTERNAL_SECRET"]

    with pytest.raises(ImproperlyConfigured) as exc:
        get_config()
    assert "INTERNAL_SECRET" in str(exc.value)


def test_non_string_internal_secret_raises(apply_settings: dict) -> None:
    """A non-string INTERNAL_SECRET is rejected."""
    from websocket_gateway._config import get_config

    apply_settings["INTERNAL_SECRET"] = 12345

    with pytest.raises(ImproperlyConfigured, match="must be a string"):
        get_config()


def test_short_internal_secret_raises_without_echoing_value(apply_settings: dict) -> None:
    """A < 32-char secret is rejected AND the value never appears in the error."""
    from websocket_gateway._config import get_config

    short_secret = "too-short-secret"
    apply_settings["INTERNAL_SECRET"] = short_secret

    with pytest.raises(ImproperlyConfigured) as exc:
        get_config()

    message = str(exc.value)
    assert "32" in message
    assert short_secret not in message, "secret value leaked in error message"


def test_secret_equal_to_django_secret_key_raises(
    apply_settings: dict,
) -> None:
    """INTERNAL_SECRET must not equal SECRET_KEY (and the value must not leak)."""
    from websocket_gateway._config import get_config

    shared = "x" * 64
    settings.SECRET_KEY = shared
    apply_settings["INTERNAL_SECRET"] = shared

    with pytest.raises(ImproperlyConfigured) as exc:
        get_config()

    message = str(exc.value)
    assert "SECRET_KEY" in message
    assert shared not in message, "secret value leaked in error message"


def test_missing_required_keys_raises(apply_settings: dict) -> None:
    """The error lists every missing required key."""
    from websocket_gateway._config import get_config

    del apply_settings["REDIS_URL"]
    del apply_settings["ALLOWED_ORIGINS"]

    with pytest.raises(ImproperlyConfigured) as exc:
        get_config()
    message = str(exc.value)
    assert "REDIS_URL" in message
    assert "ALLOWED_ORIGINS" in message


def test_invalid_callback_path_raises(apply_settings: dict) -> None:
    """An un-importable AUTHORIZATION_CALLBACK path produces a clear error."""
    from websocket_gateway._config import get_config

    apply_settings["AUTHORIZATION_CALLBACK"] = "does.not.exist.callback"

    with pytest.raises(ImproperlyConfigured) as exc:
        get_config()
    assert "does.not.exist.callback" in str(exc.value)


def test_non_callable_callback_raises(apply_settings: dict) -> None:
    """A path resolving to a non-callable is rejected."""
    from websocket_gateway._config import get_config

    apply_settings["AUTHORIZATION_CALLBACK"] = "websocket_gateway.tests._callbacks.NOT_CALLABLE"

    with pytest.raises(ImproperlyConfigured, match="not callable"):
        get_config()


def test_valid_config_returns_dict(apply_settings: dict) -> None:
    """A complete, valid config is returned unchanged."""
    from websocket_gateway._config import get_config

    result = get_config()
    assert result is apply_settings


def test_compare_digest_used_for_secret_compare() -> None:
    """Regression guard: _config uses hmac.compare_digest, never ``==``."""
    import inspect

    from websocket_gateway import _config

    source = inspect.getsource(_config)
    assert "hmac.compare_digest" in source, "must use timing-safe compare for secrets"
