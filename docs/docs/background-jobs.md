# Background jobs

The `publish()` helper works from any context with Django settings
loaded — including Celery, RQ, Django-Q, custom asyncio tasks, and
management commands.

## Celery example

```python
# myapp/tasks.py
from celery import shared_task
from websocket_gateway import publish


@shared_task
def export_report(user_id, report_id):
    # ... long-running work ...
    publish(
        f"user-{user_id}",
        {"type": "report_ready", "report_id": report_id},
    )
```

The task can run on any worker on any host. As long as it can reach the
same Redis the gateway is subscribed to, the message is delivered.

## Broker vs pub/sub on the same Redis

Celery's broker uses Redis differently from `websocket_gateway`. Both
can share a Redis instance, but use **different DB numbers** to avoid
key collisions:

```python
# settings.py
CELERY_BROKER_URL = "redis://redis:6379/1"

WEBSOCKET_GATEWAY = {
    # ...
    "REDIS_URL": "redis://redis:6379/0",
}
```

Pub/sub is in fact not scoped by DB number in Redis, but using separate
DB numbers for the broker keeps key inspection (`KEYS *`) sane.

## Progress reporting

Pair `publish()` with a Celery task's existing progress reporting:

```python
@shared_task(bind=True)
def import_csv(self, user_id, path):
    rows = open(path).readlines()
    for i, row in enumerate(rows):
        process(row)
        if i % 100 == 0:
            publish(
                f"user-{user_id}",
                {"type": "progress", "done": i, "total": len(rows)},
            )
    publish(
        f"user-{user_id}",
        {"type": "progress", "done": len(rows), "total": len(rows), "complete": True},
    )
```

## Limitation: no buffering for offline users

If the user disconnects before the task publishes, the message is lost
(v1 is non-persistent). For important state, store it in your database
and have the client fetch it on (re)connect; use `publish()` as a
prompt, not a delivery guarantee:

```python
def notify(user_id, payload):
    Notification.objects.create(user_id=user_id, **payload)
    publish(f"user-{user_id}", {"type": "new_notification"})
```

The client sees `new_notification`, hits the REST endpoint, and renders
the persisted state.
