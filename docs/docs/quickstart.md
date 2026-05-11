# Quickstart

Five minutes from `pip install` to a real-time message reaching a browser.

## 1. Install

```bash
pip install django-websocket-gateway
```

## 2. Configure Django

Add `websocket_gateway` to `INSTALLED_APPS` and set the package's settings dict:

```python
# settings.py
INSTALLED_APPS = [
    # ...
    "websocket_gateway",
]

WEBSOCKET_GATEWAY = {
    "INTERNAL_SECRET": env("WS_INTERNAL_SECRET"),
    "REDIS_URL": env("REDIS_URL", default="redis://localhost:6379/0"),
    "AUTHORIZATION_CALLBACK": "myapp.permissions.channels_for_user",
    "ALLOWED_ORIGINS": ["https://app.example.com"],
}
```

Include the package's URLs:

```python
# urls.py
urlpatterns = [
    # ...
    path("", include("websocket_gateway.urls")),
]
```

Define the authorization callback. It receives a `User` and returns the
list of channels that user may subscribe to:

```python
# myapp/permissions.py
def channels_for_user(user):
    channels = [f"user-{user.id}"]
    channels += [f"org-{o.id}" for o in user.organizations.all()]
    return channels
```

## 3. Generate the shared secret

```bash
python -c "import secrets; print(secrets.token_urlsafe(48))"
```

Set the result as `WS_INTERNAL_SECRET` in your environment. It must be at
least 32 characters and must NOT equal Django's `SECRET_KEY` — the
[settings validator](authentication.md) refuses to start otherwise.

## 4. Run

```
# Procfile
web:     gunicorn myapp.wsgi
worker:  celery -A myapp worker
gateway: python manage.py runwsgateway
```

The first invocation of `runwsgateway` downloads the Go binary from GitHub
Releases (about 15 MB) and verifies its SHA-256 before caching it under
the package's `bin/` directory.

## 5. Publish and subscribe

From anywhere in Django — views, signals, Celery tasks:

```python
from websocket_gateway import publish

publish("user-42", {"type": "notification", "text": "Your order shipped"})
```

From the browser, using the bundled JS client:

```javascript
import { WSClient } from "/static/websocket_gateway/client.js";

const ws = new WSClient(`wss://${location.host}/ws/`);
ws.on("user-42", (payload) => console.log(payload));
ws.subscribe("user-42");
```

That's the integration. See [Architecture](architecture.md) and
[Deployment](deployment.md) for production setup.

## Local development

For contributors who want to run the package from a checkout, activate
the pre-commit hook once after clone:

```bash
git config core.hooksPath .githooks
```

The hook runs ruff, gofmt, go vet, and a secret-leak scan on every commit.
