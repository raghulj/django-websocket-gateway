# Channels

Channels are the routing key the gateway uses to fan messages out. The
authorization callback is what decides which channels a given user may
subscribe to.

## Naming rules

The gateway accepts channel names matching the regex:

```
^[a-zA-Z0-9_:-]{1,128}$
```

In plain English:

- ASCII letters, digits, underscore, colon, dash.
- 1 to 128 characters.
- **No spaces, no dots, no slashes.**

Channels starting with `_` are **reserved** for internal control
messages (logout revocation, force-logout). Client-initiated
subscriptions to underscore-prefixed channels are rejected with an
error frame. The gateway auto-subscribes registered clients to their
own control channels: `_session:{session_key}` and
`_user:{user_id}:revoke`.

## Conventions

The package does not enforce a naming convention beyond the regex, but
the patterns below scale well:

| Pattern | Use |
|---|---|
| `user-{id}` | Per-user notifications (orders, mentions, DMs). |
| `org-{id}` | Organisation-wide updates. |
| `room-{slug}` | Chat rooms, collaborative documents. |
| `feature-flag-{name}` | Live feature-flag updates. |
| `system-broadcast` | Anything addressed to all logged-in users. |

`AUTHORIZATION_CALLBACK` is what makes a channel meaningful: a user gets
to see exactly the channels the callback returns for them, and no
others.

## The authorization callback

A dotted path to a callable that takes a Django user and returns
`list[str]`:

```python
# myapp/permissions.py
def channels_for_user(user):
    channels = [f"user-{user.id}"]
    channels += [f"org-{o.id}" for o in user.organizations.all()]
    if user.is_staff:
        channels.append("system-broadcast")
    return channels
```

Connect it via settings:

```python
WEBSOCKET_GATEWAY = {
    # ...
    "AUTHORIZATION_CALLBACK": "myapp.permissions.channels_for_user",
}
```

Contract:

- The callable is invoked **once per WebSocket connection**, at
  handshake time.
- The returned list is the **complete** allow-list. Channels not in the
  list are forbidden; an attempt to subscribe gets a JSON error frame
  back from the gateway.
- Mutating the list mid-connection has no effect. The user must
  reconnect to pick up new permissions. (The bundled JS client
  reconnects automatically on transient drops.)
- Returning anything other than `list[str]` raises `TypeError` and
  causes the auth view to return HTTP 500. This is intentional: it's a
  programming error in your code, not a runtime condition.

## Subscribing from the client

```javascript
import { WSClient } from "/static/websocket_gateway/client.js";

const ws = new WSClient(`wss://${location.host}/ws/`);
ws.on("user-42", (payload) => console.log("notif:", payload));
ws.subscribe("user-42");
```

`subscribe` can be called before the WebSocket has opened — the client
queues the channel and sends the subscribe frame as soon as the
connection is ready.

## Subscribing from the server's perspective

The Go gateway holds a hub that refcounts subscriptions:

- Multiple clients subscribed to the same channel → one Redis
  `SUBSCRIBE`.
- Last client leaves a channel → one Redis `UNSUBSCRIBE`.

This keeps the Redis subscription set proportional to the number of
**distinct channels currently in use**, not to the number of clients.
