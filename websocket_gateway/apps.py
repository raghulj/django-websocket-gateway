"""Django app configuration.

The :class:`WebsocketGatewayConfig` runs on Django startup. Its
:meth:`ready` hook performs two jobs:

1. **Validate the configuration eagerly.** Calling
   :func:`websocket_gateway._config.get_config` raises
   :class:`~django.core.exceptions.ImproperlyConfigured` if anything is wrong,
   so misconfiguration surfaces at deploy time rather than on the first
   WebSocket handshake.
2. **Connect the logout signal receiver** that publishes a revoke message
   when a user logs out. The receiver uses a stable ``dispatch_uid`` so
   reconnecting it is idempotent.
"""

from __future__ import annotations

from django.apps import AppConfig

from . import _config


class WebsocketGatewayConfig(AppConfig):
    """AppConfig for ``websocket_gateway``."""

    name = "websocket_gateway"
    verbose_name = "WebSocket Gateway"

    def ready(self) -> None:
        """Validate settings and wire signals. Called once by Django on startup."""
        from . import revocation

        _config.get_config()
        revocation.connect_signals()
