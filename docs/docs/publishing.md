# Publishing

The `publish()` helper sends a single message to a channel. It is the
only API you need from Django to deliver real-time messages.

```python
from websocket_gateway import publish

publish("user-42", {"type": "notification", "text": "Your order shipped"})
```

## Where to call it

Anywhere in Django that has a fully-initialised settings stack:

| Place | Example |
|---|---|
| View | After saving a model, push an update to the room. |
| Signal | `post_save` on `Notification` publishes to `user-{id}`. |
| Celery task | After a long job, publish progress or completion. |
| Management command | Bulk-broadcast an announcement. |

## Semantics

- **Fire and forget.** `publish()` returns the number of Redis
  subscribers that received the message. 0 is normal — it means no
  gateway process currently has any client listening on that channel.
- **No persistence in v1.** A client disconnected at the moment of
  publish does not see the message. Reconnects do not replay missed
  messages.
- **Order across channels is not guaranteed.** Within a single channel
  the order Redis publishes them is the order clients see them.

## Payload shape

`publish(channel, payload)` writes this JSON to Redis:

```json
{"channel": "user-42", "payload": {...}}
```

The gateway forwards the entire envelope to every subscribed client.
The bundled JS client unwraps it and hands the `payload` dict to the
registered handler for that channel.

Use a `type` field on the payload so client-side handlers can dispatch:

```python
publish("user-42", {"type": "notification", "text": "..."})
publish("user-42", {"type": "presence", "user_id": 7, "online": True})
```

## Thread safety and connection reuse

The helper holds a module-level `redis.Redis` client built lazily on
first call. Construction is thread-safe via a lock; subsequent calls
share the connection pool. There is no per-call setup cost beyond a
JSON serialise.

## Example: notifying a user after a model save

```python
from django.db.models.signals import post_save
from django.dispatch import receiver
from websocket_gateway import publish
from myapp.models import Order


@receiver(post_save, sender=Order)
def order_shipped(sender, instance, created, **kwargs):
    if not created and instance.status == "shipped":
        publish(
            f"user-{instance.user_id}",
            {"type": "order_shipped", "order_id": instance.pk},
        )
```

## API reference

::: websocket_gateway.publish.publish
