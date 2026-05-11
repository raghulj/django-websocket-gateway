# JavaScript client

The package ships a single static file: `client.js`. It exposes a
`WSClient` class with reconnect-with-backoff, channel subscription, and
sensible handling of the 4401 (revoked) close code.

It is delivered by Django's staticfiles app at
`/static/websocket_gateway/client.js`.

## Import

```javascript
import { WSClient } from "/static/websocket_gateway/client.js";
```

The file is a plain ES module — no bundler, no npm install.

## Construct

```javascript
const ws = new WSClient(`wss://${location.host}/ws/`);
```

Optional: pre-populate channels to subscribe to on each (re)connect:

```javascript
const ws = new WSClient(`wss://${location.host}/ws/`, {
  channels: ["user-42", "org-1"],
});
```

## Subscribe and handle

```javascript
ws.on("user-42", (payload) => {
  console.log("notification:", payload);
});
ws.subscribe("user-42");
```

`on(channel, handler)` registers (or replaces) a handler. The handler
receives the **payload** — the gateway envelope (`{channel, payload}`)
is unwrapped for you.

`subscribe(channel)` can be called before the connection has opened.
The channel is queued and subscribed as soon as the connection is ready.

## Unsubscribe

```javascript
ws.unsubscribe("user-42");
```

The channel is removed from the set; subsequent reconnects will not
re-subscribe to it.

## Close

```javascript
ws.close();
```

Closes the connection and disables auto-reconnect. Useful when
navigating away from a route that owned the subscriptions.

## Reconnect behaviour

| Close code | Action |
|---|---|
| 4401 (session revoked) | Stop. The browser is no longer authorised. |
| Anything else | Reconnect with exponential backoff: 1s, 2s, 4s, …, jittered, capped at 30s. |

After a successful (re)connect, the client re-sends `subscribe` for
every channel currently in its set. Handlers stay registered across
reconnects.

## Error frames

If you attempt to subscribe to a channel that is not in your allow-list
(per `AUTHORIZATION_CALLBACK`), the gateway sends:

```json
{"type": "error", "channel": "forbidden-channel", "reason": "forbidden"}
```

The client logs a `console.warn` and does **not** call your handler.

## Source

```javascript
--8<-- "websocket_gateway/static/websocket_gateway/client.js"
```
