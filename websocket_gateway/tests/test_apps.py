"""Tests for ``WebsocketGatewayConfig.ready``."""

from __future__ import annotations

from typing import Any

import pytest
from django.contrib.auth.signals import user_logged_out
from django.core.exceptions import ImproperlyConfigured


def test_ready_validates_config_and_raises_on_invalid(monkeypatch: Any) -> None:
    """ready() invokes _config.get_config() and propagates ImproperlyConfigured."""
    from websocket_gateway import apps

    def boom() -> dict:
        raise ImproperlyConfigured("nope")

    monkeypatch.setattr(apps._config, "get_config", boom)

    cfg = apps.WebsocketGatewayConfig.create("websocket_gateway")
    with pytest.raises(ImproperlyConfigured, match="nope"):
        cfg.ready()


def test_ready_connects_logout_signal(apply_settings: dict, monkeypatch: Any) -> None:
    """ready() registers the logout receiver exactly once even when called twice."""
    from websocket_gateway import apps

    user_logged_out.disconnect(dispatch_uid="websocket_gateway.revoke_on_logout")

    cfg = apps.WebsocketGatewayConfig.create("websocket_gateway")
    cfg.ready()
    cfg.ready()

    matches = [
        r for r in user_logged_out.receivers if r[0][0] == "websocket_gateway.revoke_on_logout"
    ]
    assert len(matches) == 1
