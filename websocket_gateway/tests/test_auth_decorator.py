"""Tests for the ``X-Internal-Auth`` header check."""

from __future__ import annotations

import inspect
import logging
from typing import Any

import pytest
from django.http import HttpResponse
from django.test import RequestFactory


def _make_request(factory: RequestFactory, header_value: str | None = None) -> Any:
    headers: dict[str, str] = {}
    if header_value is not None:
        headers["HTTP_X_INTERNAL_AUTH"] = header_value
    return factory.post("/internal/ws-auth/", **headers)


def _wrapped_view() -> Any:
    from websocket_gateway.auth_decorator import require_internal_auth

    @require_internal_auth
    def view(request: Any) -> HttpResponse:
        return HttpResponse("ok", status=200)

    return view


@pytest.fixture
def factory() -> RequestFactory:
    return RequestFactory()


def test_missing_header_is_forbidden(apply_settings: dict, factory: RequestFactory) -> None:
    """A request without ``X-Internal-Auth`` is rejected with 403."""
    response = _wrapped_view()(_make_request(factory))
    assert response.status_code == 403


def test_wrong_secret_is_forbidden(apply_settings: dict, factory: RequestFactory) -> None:
    """A request with the wrong secret is rejected with 403."""
    response = _wrapped_view()(_make_request(factory, header_value="not-the-secret"))
    assert response.status_code == 403


def test_correct_secret_passes_through(apply_settings: dict, factory: RequestFactory) -> None:
    """A request with the correct secret reaches the wrapped view."""
    response = _wrapped_view()(
        _make_request(factory, header_value=apply_settings["INTERNAL_SECRET"])
    )
    assert response.status_code == 200
    assert response.content == b"ok"


def test_failed_auth_log_does_not_contain_secret_value(
    apply_settings: dict,
    factory: RequestFactory,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Log messages naming a rejection never contain the provided or expected secret."""
    provided = "guessed-secret-attempt"
    with caplog.at_level(logging.WARNING, logger="websocket_gateway.auth_decorator"):
        _wrapped_view()(_make_request(factory, header_value=provided))

    captured = "\n".join(record.getMessage() for record in caplog.records)
    assert "rejected" in captured.lower()
    assert provided not in captured
    assert apply_settings["INTERNAL_SECRET"] not in captured


def test_uses_compare_digest() -> None:
    """Regression guard: the decorator uses hmac.compare_digest, not ``==``."""
    from websocket_gateway import auth_decorator

    source = inspect.getsource(auth_decorator)
    assert "hmac.compare_digest" in source
    assert "==" not in source.replace("!=", "")  # crude but effective for this tiny module
