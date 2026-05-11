"""Tests for the top-level ``websocket_gateway`` import surface."""

from __future__ import annotations


def test_top_level_exports_publish_and_force_logout_and_version() -> None:
    """The three documented exports import directly from the package root."""
    from websocket_gateway import __version__, force_logout_user, publish

    assert callable(publish)
    assert callable(force_logout_user)
    assert __version__ == "0.1.0"


def test_default_app_config_points_to_websocket_gateway_apps() -> None:
    """default_app_config still works for projects that don't auto-discover."""
    import websocket_gateway

    assert websocket_gateway.default_app_config == "websocket_gateway.apps.WebsocketGatewayConfig"
