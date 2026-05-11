# Logout & revocation

When a user logs out, their live WebSocket connections close within
about 100 ms. There are two pathways: the automatic `user_logged_out`
signal handler, and the explicit `force_logout_user(user)` for
administrative actions like bans.

## Automatic: Django's logout flow

The package's `AppConfig.ready()` connects a receiver to
[`user_logged_out`][signal]. When Django's `LogoutView` (or any code
calling `django.contrib.auth.logout()`) fires the signal, the receiver
publishes:

```json
{"channel": "_session:<session_key>", "payload": {"type": "revoke", "reason": "logout"}}
```

[signal]: https://docs.djangoproject.com/en/stable/ref/contrib/auth/#django.contrib.auth.signals.user_logged_out

Each connected gateway sees the message on the `_session:{key}` control
channel and closes the matching WebSocket with code 4401. The bundled
JS client sees 4401 and stops reconnecting.

The receiver is connected with a stable `dispatch_uid` so multi-process
servers (gunicorn, uwsgi) reconnecting `ready()` do not register
duplicate handlers.

## Manual: force_logout_user

Use when you need to kick **every** connection for a user, regardless
of session — bans, password rotation, security events:

```python
from websocket_gateway import force_logout_user

force_logout_user(banned_user)
```

This publishes on `_user:{pk}:revoke`. The gateway auto-subscribes each
connection to its user's revoke channel at handshake time, so the
disconnect reaches every active WebSocket the user has open.

`force_logout_user` does **not** delete Django's session rows. If you
also need to invalidate the session cookie itself:

```python
from django.contrib.sessions.models import Session

Session.objects.filter(...).delete()
force_logout_user(banned_user)
```

## Behaviour matrix

| Event | WebSocket result | Browser behaviour |
|---|---|---|
| User clicks "Log out" | Closed with 4401 within ~100 ms | JS client stops reconnecting |
| Admin calls `force_logout_user(u)` | Every connection for `u` closed with 4401 | Same |
| Session cookie expires (Django setting `SESSION_COOKIE_AGE`) | No effect on live WS; close happens on next page navigation | Reconnect with no cookie → 401 → JS client stops |
| User closes browser tab | TCP FIN → gateway closes cleanly | — |
| Gateway process SIGTERM | All connections closed with 1001 within 10 s | JS client reconnects to whichever replica accepts |

## Implementation reference

::: websocket_gateway.revocation
    options:
      members: [force_logout_user, connect_signals]
