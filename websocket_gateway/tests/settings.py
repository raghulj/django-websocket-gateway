"""Django settings used only by the package test suite.

The values here are deliberately minimal and unsafe-for-production. They exist
so that ``pytest-django`` can configure Django before importing the package
under test. Individual tests override the ``WEBSOCKET_GATEWAY`` dict as needed
via fixtures in ``conftest.py``.
"""

from __future__ import annotations

SECRET_KEY = "django-test-secret-key-padded-to-be-long-enough-for-tests"

DEBUG = False

INSTALLED_APPS = [
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "websocket_gateway",
]

DATABASES = {
    "default": {
        "ENGINE": "django.db.backends.sqlite3",
        "NAME": ":memory:",
    },
}

USE_TZ = True

ROOT_URLCONF = "websocket_gateway.tests.urls"

DEFAULT_AUTO_FIELD = "django.db.models.BigAutoField"

# Placeholder valid config so AppConfig.ready() succeeds during Django startup.
# Individual tests override or delete this via the apply_settings fixture in
# conftest.py.
WEBSOCKET_GATEWAY = {
    "INTERNAL_SECRET": "default-startup-secret-distinct-from-django-secret-key-padded",
    "REDIS_URL": "redis://localhost:6379/0",
    "AUTHORIZATION_CALLBACK": "websocket_gateway.tests._callbacks.allow_test_channel",
    "ALLOWED_ORIGINS": ["https://app.example.com"],
}
