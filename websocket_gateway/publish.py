"""Publish messages to WebSocket channels from anywhere in Django.

The :func:`publish` helper takes a channel name and a JSON-serialisable payload
dict and pushes a single Redis ``PUBLISH`` to the gateway's pub/sub backplane.
The gateway then fans the message out to every connected client subscribed to
that channel. Senders do not need to know whether anyone is listening —
``publish`` to an empty channel is not an error.

Use it from views, signals, Celery tasks, management commands, or any other
Django-aware code:

.. code-block:: python

    from websocket_gateway import publish

    publish("user-42", {"type": "notification", "text": "Your order shipped"})

A single Redis client is shared across all callers and lazily built on first
use. Construction is thread-safe.
"""

from __future__ import annotations

import json
import threading
from typing import Any

import redis

from ._config import get_config

_lock = threading.Lock()
_client: redis.Redis | None = None


def _get_client() -> redis.Redis:
    """Return the cached Redis client, building it on first call.

    Thread-safe via a module-level lock. The double-checked pattern is used so
    the fast path (client already constructed) avoids the lock entirely.
    """
    global _client
    if _client is not None:
        return _client
    with _lock:
        if _client is None:
            _client = redis.from_url(get_config()["REDIS_URL"])
        return _client


def publish(channel: str, payload: dict[str, Any]) -> int:
    """Publish ``payload`` on ``channel`` via Redis pub/sub.

    The wire format is a single JSON document of the form
    ``{"channel": "<name>", "payload": <payload>}``. The gateway parses this
    envelope, looks up local subscribers for ``channel``, and forwards the
    payload to each.

    Args:
        channel: Channel name. Must match the gateway's regex
            ``^[a-zA-Z0-9_:-]{1,128}$`` for client-facing channels. Channels
            beginning with ``_`` are reserved for internal control messages
            (used by :func:`websocket_gateway.force_logout_user` and the
            logout signal handler).
        payload: A JSON-serialisable dict. Use ``"type"`` as a discriminator
            so client-side handlers can dispatch on the shape.

    Returns:
        The number of Redis subscribers that received the message. This is
        the number of gateway processes subscribed, not the number of
        end-user WebSocket connections; a value of 0 means no gateway is
        currently listening on this channel.

    Example:
        >>> from websocket_gateway import publish
        >>> publish("user-42", {"type": "notification", "text": "hi"})
        1
    """
    message = json.dumps({"channel": channel, "payload": payload})
    return _get_client().publish(channel, message)
