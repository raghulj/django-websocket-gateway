"""Session-revocation: kick live WebSocket connections when a user logs out.

Two entry points feed the same downstream channel pattern:

* :func:`connect_signals` wires :data:`django.contrib.auth.signals.user_logged_out`
  to :func:`_revoke_on_logout`, which publishes a small "revoke" envelope on
  the session-scoped control channel ``_session:{session_key}``.
* :func:`force_logout_user` publishes on the user-scoped control channel
  ``_user:{pk}:revoke`` to disconnect every live connection a user has —
  useful for bans, security events, or "log out everywhere" buttons.

Both control channels are reserved (the ``_`` prefix is rejected by the
gateway for client subscriptions). The gateway auto-subscribes each accepted
connection to its session and user control channels at handshake time and
closes the WebSocket with code ``4401`` when it sees a revoke message.
"""

from __future__ import annotations

from typing import Any

from django.contrib.auth.signals import user_logged_out

from .publish import publish


def connect_signals() -> None:
    """Connect the logout receiver. Safe to call multiple times.

    Uses a stable ``dispatch_uid`` so repeated calls (for example, from a
    second :meth:`~django.apps.AppConfig.ready` invocation in a multi-process
    server) do not register the receiver twice.
    """
    user_logged_out.connect(
        _revoke_on_logout,
        dispatch_uid="websocket_gateway.revoke_on_logout",
    )


def _revoke_on_logout(sender: Any, request: Any, user: Any, **kwargs: Any) -> None:
    """Publish a revoke envelope when a user logs out.

    No-ops if ``request`` is None (the signal can be fired manually with no
    request context) or if the request's session has no ``session_key``
    (anonymous logout has nothing to revoke).
    """
    if request is None or not getattr(request, "session", None):
        return
    session_key = request.session.session_key
    if session_key:
        publish(f"_session:{session_key}", {"type": "revoke", "reason": "logout"})


def force_logout_user(user: Any) -> None:
    """Force-close every live WebSocket connection for ``user``.

    Use after revoking access (banning, security event, password rotation).
    This does **not** delete the user's Django session rows — call
    ``Session.objects.filter(...).delete()`` yourself if you also need to
    invalidate the cookie. ``force_logout_user`` only kicks live WebSocket
    connections.

    Args:
        user: The Django user instance. ``user.pk`` is used to address the
            user-scoped control channel.

    Example:
        >>> from websocket_gateway import force_logout_user
        >>> force_logout_user(banned_user)
    """
    publish(f"_user:{user.pk}:revoke", {"type": "revoke", "reason": "force_logout"})
