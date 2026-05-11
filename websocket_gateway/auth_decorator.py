"""Django view decorator that enforces the ``X-Internal-Auth`` shared secret.

Apply :func:`require_internal_auth` to any view that should be reachable only
by the trusted internal caller (the Go gateway, or another backend process)
that holds the configured ``INTERNAL_SECRET``.

The comparison is timing-safe (:func:`hmac.compare_digest`). Rejected requests
log a structured warning that names the failure mode (missing header vs
mismatch) and the remote address, but **never** the value of the provided or
expected secret.
"""

from __future__ import annotations

import functools
import hmac
import logging
from collections.abc import Callable
from typing import Any

from django.http import HttpResponseForbidden

from ._config import get_config

logger = logging.getLogger(__name__)


def require_internal_auth(view_func: Callable[..., Any]) -> Callable[..., Any]:
    """Wrap ``view_func`` so it only runs when the request bears the secret.

    The view runs only if the request includes an ``X-Internal-Auth`` header
    whose value matches ``WEBSOCKET_GATEWAY['INTERNAL_SECRET']``. The check
    uses :func:`hmac.compare_digest` to defeat timing attacks.

    Args:
        view_func: The Django view callable to protect.

    Returns:
        A wrapper view that returns :class:`~django.http.HttpResponseForbidden`
        (HTTP 403) when the header is missing or wrong, and delegates to
        ``view_func`` otherwise.
    """

    @functools.wraps(view_func)
    def wrapper(request: Any, *args: Any, **kwargs: Any) -> Any:
        cfg = get_config()
        provided = request.headers.get("X-Internal-Auth", "")
        if not provided:
            logger.warning(
                "ws-auth rejected: missing X-Internal-Auth header",
                extra={"remote": request.META.get("REMOTE_ADDR")},
            )
            return HttpResponseForbidden()
        if not hmac.compare_digest(provided, cfg["INTERNAL_SECRET"]):
            logger.warning(
                "ws-auth rejected: invalid X-Internal-Auth header",
                extra={"remote": request.META.get("REMOTE_ADDR")},
            )
            return HttpResponseForbidden()
        return view_func(request, *args, **kwargs)

    return wrapper
