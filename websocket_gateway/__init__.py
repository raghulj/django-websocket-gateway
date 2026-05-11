"""Drop-in real-time WebSocket gateway for Django.

This package's public surface is intentionally small:

* :func:`websocket_gateway.publish` — send a message to a channel from
  views, signals, or background workers.
* :func:`websocket_gateway.force_logout_user` — kick every live WebSocket
  connection for a user (bans, security events, "log out everywhere").
* :data:`websocket_gateway.__version__` — package version.

See the project documentation for architecture, configuration, deployment,
and the threat model.
"""

from __future__ import annotations

from ._version import __version__
from .publish import publish
from .revocation import force_logout_user

default_app_config = "websocket_gateway.apps.WebsocketGatewayConfig"

__all__ = ["__version__", "default_app_config", "force_logout_user", "publish"]
