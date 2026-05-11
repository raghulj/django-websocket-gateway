"""Settings access and startup validation for ``websocket_gateway``.

The package reads all configuration from a single Django setting,
``WEBSOCKET_GATEWAY``, which is a ``dict`` mapping uppercase keys to values.
:func:`get_config` validates the dict on every call (cheap; no I/O) and raises
:class:`~django.core.exceptions.ImproperlyConfigured` with an actionable
message when something is wrong.

Validation enforces the hard rules from ``CLAUDE.md``:

* ``INTERNAL_SECRET`` must be present, a string, and at least 32 characters
  long. The value itself never appears in any error message — only the rule
  that was violated.
* ``INTERNAL_SECRET`` must not equal Django's ``SECRET_KEY``. The comparison
  uses :func:`hmac.compare_digest` to avoid leaking timing information.
* ``REDIS_URL``, ``AUTHORIZATION_CALLBACK``, and ``ALLOWED_ORIGINS`` are
  required.
* ``AUTHORIZATION_CALLBACK`` must resolve to a callable.

Required settings:

================================  ============================================
Key                               Meaning
================================  ============================================
``INTERNAL_SECRET``               Dedicated shared secret for the
                                  Django↔gateway internal API. Distinct from
                                  ``SECRET_KEY``. ≥ 32 characters.
``REDIS_URL``                     ``redis://...`` URL for pub/sub.
``AUTHORIZATION_CALLBACK``        Dotted path to a callable
                                  ``callback(user) -> list[str]`` returning
                                  the channels a user may subscribe to.
``ALLOWED_ORIGINS``               List of WebSocket ``Origin`` headers the
                                  gateway will accept.
================================  ============================================

Example:

.. code-block:: python

    WEBSOCKET_GATEWAY = {
        "INTERNAL_SECRET": env("WS_INTERNAL_SECRET"),
        "REDIS_URL": env("REDIS_URL"),
        "AUTHORIZATION_CALLBACK": "myapp.permissions.channels_for_user",
        "ALLOWED_ORIGINS": ["https://app.example.com"],
    }
"""

from __future__ import annotations

import hmac
from typing import Any

from django.conf import settings
from django.core.exceptions import ImproperlyConfigured

MIN_SECRET_LENGTH = 32
"""Minimum length, in characters, accepted for ``INTERNAL_SECRET``."""

_REQUIRED_KEYS = ("REDIS_URL", "AUTHORIZATION_CALLBACK", "ALLOWED_ORIGINS")


def get_config() -> dict[str, Any]:
    """Return the validated ``WEBSOCKET_GATEWAY`` settings dict.

    The returned object is the same dict configured by the application; it is
    not a defensive copy. Mutating it is supported (and used by the test
    suite) but discouraged in application code.

    Returns:
        The configured ``settings.WEBSOCKET_GATEWAY`` dict.

    Raises:
        ImproperlyConfigured: When any required key is missing, the shared
            secret is invalid, or ``AUTHORIZATION_CALLBACK`` does not resolve
            to a callable. The exception message describes the problem
            without echoing any secret value.
    """
    cfg = getattr(settings, "WEBSOCKET_GATEWAY", None)
    if cfg is None:
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY settings dict is required when "
            "'websocket_gateway' is in INSTALLED_APPS."
        )
    _validate_secret(cfg)
    _validate_required(cfg)
    _validate_callback(cfg)
    return cfg


def _validate_secret(cfg: dict[str, Any]) -> None:
    """Validate ``INTERNAL_SECRET`` without leaking its value into errors."""
    secret = cfg.get("INTERNAL_SECRET")
    if not secret:
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY['INTERNAL_SECRET'] is required. "
            'Generate one with: python -c "import secrets; '
            'print(secrets.token_urlsafe(48))"'
        )
    if not isinstance(secret, str):
        raise ImproperlyConfigured("WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must be a string.")
    if len(secret) < MIN_SECRET_LENGTH:
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must be at least "
            f"{MIN_SECRET_LENGTH} characters long."
        )
    django_secret = settings.SECRET_KEY or ""
    if (
        isinstance(django_secret, str)
        and django_secret
        and hmac.compare_digest(secret, django_secret)
    ):
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must NOT equal "
            "settings.SECRET_KEY. Use a distinct, dedicated secret."
        )


def _validate_required(cfg: dict[str, Any]) -> None:
    """Ensure non-secret required keys are present."""
    missing = [k for k in _REQUIRED_KEYS if not cfg.get(k)]
    if missing:
        raise ImproperlyConfigured(f"WEBSOCKET_GATEWAY missing required keys: {', '.join(missing)}")


def _validate_callback(cfg: dict[str, Any]) -> None:
    """Resolve ``AUTHORIZATION_CALLBACK`` and confirm it is callable."""
    from django.utils.module_loading import import_string

    path = cfg["AUTHORIZATION_CALLBACK"]
    try:
        callback = import_string(path)
    except ImportError as exc:
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['AUTHORIZATION_CALLBACK']='{path}' could not be imported: {exc}"
        ) from exc
    if not callable(callback):
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['AUTHORIZATION_CALLBACK']='{path}' is not callable."
        )
