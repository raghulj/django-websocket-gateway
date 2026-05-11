"""HTTP views exposed by the package.

The single view :func:`ws_auth` is the private endpoint the Go gateway calls
during the WebSocket handshake. It validates the forwarded session ID, looks
up the user, invokes the configured authorization callback, and returns a
JSON body describing whether the connection is authenticated and which
channels it may subscribe to.

The view is locked down by three layers:

1. **Network**: Caddy is configured to return 404 for any public request to
   ``/internal/*``. Only the gateway, on the internal network, can reach it.
2. **Header**: :func:`~websocket_gateway.auth_decorator.require_internal_auth`
   rejects any request missing the dedicated shared secret in
   ``X-Internal-Auth``. Removing the network layer still leaves this in place.
3. **Method + CSRF**: ``@require_POST`` + ``@csrf_exempt`` — the endpoint is a
   server-to-server POST, not a browser-facing form.
"""

from __future__ import annotations

from typing import Any

from django.contrib.auth import get_user_model
from django.contrib.sessions.backends.db import SessionStore
from django.http import JsonResponse
from django.utils.module_loading import import_string
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_POST

from ._config import get_config
from .auth_decorator import require_internal_auth


@csrf_exempt
@require_POST
@require_internal_auth
def ws_auth(request: Any) -> JsonResponse:
    """Authenticate a WebSocket handshake on behalf of the gateway.

    Request:
        Method: ``POST``
        Headers:
            * ``X-Internal-Auth``: the configured ``INTERNAL_SECRET``.
            * ``X-Forwarded-Session``: the value of the user's ``sessionid``
              cookie, forwarded by the gateway.

    Returns:
        * ``200`` + ``{"authenticated": true, "user_id": int, "username": str,
          "allowed_channels": list[str]}`` for a valid, active user.
        * ``401`` + ``{"authenticated": false}`` for missing/invalid session
          or an inactive user.
        * ``403`` (via :func:`require_internal_auth`) when the
          ``X-Internal-Auth`` header is missing or wrong.

    Raises:
        TypeError: When ``AUTHORIZATION_CALLBACK`` returns a value that is
            not ``list[str]``. This indicates a programmer error in user code
            and is intentionally surfaced as HTTP 500 to make it loud.
    """
    session_key = request.headers.get("X-Forwarded-Session", "")
    if not session_key:
        return JsonResponse({"authenticated": False}, status=401)

    session = SessionStore(session_key=session_key)
    user_id = session.get("_auth_user_id")
    if not user_id:
        return JsonResponse({"authenticated": False}, status=401)

    User = get_user_model()
    try:
        user = User.objects.get(pk=user_id, is_active=True)
    except User.DoesNotExist:
        return JsonResponse({"authenticated": False}, status=401)

    cfg = get_config()
    callback = import_string(cfg["AUTHORIZATION_CALLBACK"])
    allowed_channels = callback(user)

    if not isinstance(allowed_channels, list) or not all(
        isinstance(channel, str) for channel in allowed_channels
    ):
        raise TypeError(
            f"AUTHORIZATION_CALLBACK must return list[str], got {type(allowed_channels).__name__}"
        )

    return JsonResponse(
        {
            "authenticated": True,
            "user_id": user.pk,
            "username": user.get_username(),
            "allowed_channels": allowed_channels,
        }
    )
