"""URL routes contributed by the package.

Include this module from your project's root URLconf to expose the
``/internal/ws-auth/`` endpoint:

.. code-block:: python

    urlpatterns = [
        # ...
        path("", include("websocket_gateway.urls")),
    ]

The route is **private**. Configure your reverse proxy (the example
``Caddyfile`` does this) to return 404 for any public traffic to
``/internal/*``. The view also enforces the ``X-Internal-Auth`` shared-secret
header so that even direct reach by a misconfigured proxy is rejected.
"""

from __future__ import annotations

from django.urls import path

from .views import ws_auth

app_name = "websocket_gateway"

urlpatterns = [
    path("internal/ws-auth/", ws_auth, name="ws-auth"),
]
