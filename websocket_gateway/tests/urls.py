"""URLconf for the test settings module."""

from __future__ import annotations

from django.urls import include, path

urlpatterns = [path("", include("websocket_gateway.urls"))]
