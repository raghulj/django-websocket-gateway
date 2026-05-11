"""Tests for ``websocket_gateway.publish``."""

from __future__ import annotations

import json
import threading
from typing import Any

import fakeredis


def test_publish_sends_envelope_to_redis(
    apply_settings: dict,
    patched_publish_client: fakeredis.FakeStrictRedis,
) -> None:
    """publish() invokes Redis PUBLISH once with a JSON {channel, payload} envelope."""
    from websocket_gateway.publish import publish

    pubsub = patched_publish_client.pubsub()
    pubsub.subscribe("user-42")
    # Drain the subscribe confirmation frame.
    pubsub.get_message(timeout=0.1)

    publish("user-42", {"text": "hi"})

    message = pubsub.get_message(timeout=0.1)
    assert message is not None
    assert message["channel"] == b"user-42"
    body = json.loads(message["data"])
    assert body == {"channel": "user-42", "payload": {"text": "hi"}}


def test_publish_returns_subscriber_count(
    apply_settings: dict,
    patched_publish_client: fakeredis.FakeStrictRedis,
) -> None:
    """publish() returns the value Redis returns from PUBLISH (subscriber count)."""
    from websocket_gateway.publish import publish

    pubsub = patched_publish_client.pubsub()
    pubsub.subscribe("watch")
    pubsub.get_message(timeout=0.1)

    result = publish("watch", {"a": 1})
    assert result == 1


def test_redis_client_is_cached(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """The internal Redis client is built once and reused across publish() calls."""
    import importlib

    publish_module = importlib.import_module("websocket_gateway.publish")

    monkeypatch.setattr(publish_module, "_client", None, raising=False)

    calls: list[str] = []

    def fake_from_url(url: str, *args: Any, **kwargs: Any) -> fakeredis.FakeStrictRedis:
        calls.append(url)
        return fakeredis.FakeStrictRedis()

    monkeypatch.setattr(publish_module.redis, "from_url", fake_from_url)

    publish_module.publish("ch", {"x": 1})
    publish_module.publish("ch", {"x": 2})

    assert len(calls) == 1, f"expected one redis.from_url call, got {len(calls)}"


def test_concurrent_first_calls_share_one_client(
    apply_settings: dict,
    monkeypatch: Any,
) -> None:
    """Two threads calling publish() concurrently produce one underlying client."""
    import importlib

    publish_module = importlib.import_module("websocket_gateway.publish")

    monkeypatch.setattr(publish_module, "_client", None, raising=False)

    calls: list[str] = []
    start = threading.Event()

    def fake_from_url(url: str, *args: Any, **kwargs: Any) -> fakeredis.FakeStrictRedis:
        calls.append(url)
        return fakeredis.FakeStrictRedis()

    monkeypatch.setattr(publish_module.redis, "from_url", fake_from_url)

    def worker() -> None:
        start.wait()
        publish_module.publish("ch", {"x": 1})

    threads = [threading.Thread(target=worker) for _ in range(8)]
    for t in threads:
        t.start()
    start.set()
    for t in threads:
        t.join()

    assert len(calls) == 1, f"client constructed {len(calls)} times under concurrency"
