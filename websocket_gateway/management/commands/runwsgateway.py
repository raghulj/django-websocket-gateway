"""``runwsgateway`` — launch the Go gateway as a separate process.

The command:

1. Validates the Django ``WEBSOCKET_GATEWAY`` settings dict (so misconfigured
   deployments fail fast, before the binary launches).
2. Downloads the platform-appropriate gateway binary from GitHub Releases on
   first use (subsequent invocations reuse the cached file).
3. Translates Django settings into the environment variables the Go binary
   expects (``INTERNAL_AUTH_SECRET``, ``REDIS_URL``, ``ALLOWED_ORIGINS``, …).
4. Replaces the Python process with the gateway via :func:`os.execvpe`.

The use of ``execvpe`` rather than ``subprocess.Popen`` is deliberate. The
gateway is meant to be supervised by the platform (systemd, Docker, k8s);
keeping a Python middleman around would mean two processes to monitor and an
extra failure surface for signals.

Usage:

.. code-block:: bash

    python manage.py runwsgateway
"""

from __future__ import annotations

import os
from typing import Any

from django.core.management.base import BaseCommand

from websocket_gateway import __version__
from websocket_gateway._config import get_config
from websocket_gateway._downloader import ensure_binary


class Command(BaseCommand):
    """Run the WebSocket gateway (Go binary, separate process)."""

    help = "Run the WebSocket gateway (Go binary, separate process)."

    def handle(self, *args: Any, **options: Any) -> None:
        """Resolve the binary and exec into it."""
        cfg = get_config()
        binary = ensure_binary()
        env = os.environ.copy()
        env.update(_translate(cfg))
        self.stdout.write(self.style.SUCCESS(f"Launching gateway {__version__}: {binary}"))
        os.execvpe(str(binary), [str(binary)], env)


_OPTIONAL_PASS_THROUGH = (
    "MAX_CONNECTIONS_PER_USER",
    "MAX_CONNECTIONS_TOTAL",
    "MAX_MESSAGE_SIZE",
    "PING_INTERVAL",
    "PONG_TIMEOUT",
    "CONNECTION_MAX_LIFETIME",
    "AUTH_TIMEOUT",
)


def _translate(cfg: dict[str, Any]) -> dict[str, str]:
    """Map the Django settings dict to gateway environment variables.

    Required mappings are fixed. Optional values appear in the env only when
    they are present in the settings dict; this lets the Go binary fall back
    to its built-in defaults for anything unspecified.
    """
    env: dict[str, str] = {
        "INTERNAL_AUTH_SECRET": cfg["INTERNAL_SECRET"],
        "REDIS_URL": cfg["REDIS_URL"],
        "DJANGO_AUTH_URL": cfg.get("DJANGO_AUTH_URL", "http://django:8000/internal/ws-auth/"),
        "ALLOWED_ORIGINS": ",".join(cfg["ALLOWED_ORIGINS"]),
        "LISTEN_ADDR": cfg.get("GATEWAY_BIND", ":8080"),
        "LOG_LEVEL": cfg.get("LOG_LEVEL", "info"),
    }
    for key in _OPTIONAL_PASS_THROUGH:
        if key in cfg:
            env[key] = str(cfg[key])
    return env
