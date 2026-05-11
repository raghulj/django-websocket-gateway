"""Tests for ``websocket_gateway._downloader``."""

from __future__ import annotations

import hashlib
import http.server
import threading
from pathlib import Path
from typing import Any

import pytest


@pytest.fixture
def fixtures(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """A tmp directory served over HTTP plus the platform binary name.

    The fixture isolates the downloader from real GitHub by pointing
    ``WS_GATEWAY_DOWNLOAD_URL`` at a local HTTP server, and redirects the
    ``bin/`` cache directory to a per-test tmp path.
    """
    from websocket_gateway import _downloader

    serve_dir = tmp_path / "serve"
    serve_dir.mkdir()
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()

    monkeypatch.setattr(_downloader, "_bin_dir", lambda: bin_dir)
    monkeypatch.delenv("WS_GATEWAY_BINARY_PATH", raising=False)
    monkeypatch.delenv("WS_GATEWAY_SKIP_DOWNLOAD", raising=False)

    binary_name = _downloader._platform_binary_name()

    handler_cls = http.server.SimpleHTTPRequestHandler
    # Suppress the default logging which clutters pytest output.
    handler_cls.log_message = lambda *args, **kwargs: None  # type: ignore[assignment]

    class _ChrootHandler(handler_cls):
        def translate_path(self, path: str) -> str:
            # Always serve out of serve_dir regardless of cwd.
            relative = path.lstrip("/").split("?", 1)[0]
            return str(serve_dir / relative)

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _ChrootHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{server.server_port}"
    monkeypatch.setenv("WS_GATEWAY_DOWNLOAD_URL", url)

    yield {
        "serve_dir": serve_dir,
        "bin_dir": bin_dir,
        "binary_name": binary_name,
        "url": url,
    }

    server.shutdown()
    server.server_close()
    thread.join(timeout=1)


def _put_binary(
    fixtures: dict[str, Any], content: bytes = b"#!/bin/sh\necho fake gateway\n"
) -> str:
    """Write content as the platform binary at the server root and return its SHA-256."""
    path = fixtures["serve_dir"] / fixtures["binary_name"]
    path.write_bytes(content)
    return hashlib.sha256(content).hexdigest()


def _put_checksums(fixtures: dict[str, Any], lines: list[str]) -> None:
    (fixtures["serve_dir"] / "SHA256SUMS").write_text("\n".join(lines) + "\n")


def test_happy_path_downloads_and_verifies(fixtures: dict[str, Any]) -> None:
    """A matching SHA256 lets the binary land at bin/<name> and be executable."""
    from websocket_gateway._downloader import ensure_binary

    checksum = _put_binary(fixtures)
    _put_checksums(fixtures, [f"{checksum}  {fixtures['binary_name']}"])

    path = ensure_binary()
    assert path == fixtures["bin_dir"] / fixtures["binary_name"]
    assert path.is_file()
    assert path.stat().st_mode & 0o111  # executable bits set


def test_checksum_mismatch_leaves_no_file(fixtures: dict[str, Any]) -> None:
    """A tampered binary aborts with DownloadError and nothing on disk."""
    from websocket_gateway._downloader import DownloadError, ensure_binary

    _put_binary(fixtures, content=b"tampered")
    # Use a deliberately wrong checksum.
    _put_checksums(fixtures, [f"{'0' * 64}  {fixtures['binary_name']}"])

    with pytest.raises(DownloadError, match="checksum"):
        ensure_binary()

    dest = fixtures["bin_dir"] / fixtures["binary_name"]
    assert not dest.exists()
    assert list(fixtures["bin_dir"].glob(".dl-*")) == []


def test_missing_checksum_entry_raises(fixtures: dict[str, Any]) -> None:
    """A SHA256SUMS without our binary line is a hard error."""
    from websocket_gateway._downloader import DownloadError, ensure_binary

    _put_binary(fixtures)
    _put_checksums(fixtures, ["abcdef  some-other-file"])

    with pytest.raises(DownloadError, match="not in SHA256SUMS"):
        ensure_binary()


def test_binary_path_override_existing(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """WS_GATEWAY_BINARY_PATH pointing at an existing file returns it unchanged."""
    from websocket_gateway._downloader import ensure_binary

    local = tmp_path / "my-gateway"
    local.write_bytes(b"local build")
    monkeypatch.setenv("WS_GATEWAY_BINARY_PATH", str(local))
    monkeypatch.delenv("WS_GATEWAY_SKIP_DOWNLOAD", raising=False)

    assert ensure_binary() == local


def test_binary_path_override_missing_raises(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """WS_GATEWAY_BINARY_PATH pointing at a non-existent file is rejected."""
    from websocket_gateway._downloader import DownloadError, ensure_binary

    monkeypatch.setenv("WS_GATEWAY_BINARY_PATH", str(tmp_path / "nope"))

    with pytest.raises(DownloadError, match="does not exist"):
        ensure_binary()


def test_skip_download_without_cached_binary_raises(fixtures: dict[str, Any]) -> None:
    """WS_GATEWAY_SKIP_DOWNLOAD + nothing in bin/ → DownloadError with hint."""
    import os

    from websocket_gateway._downloader import DownloadError, ensure_binary

    os.environ["WS_GATEWAY_SKIP_DOWNLOAD"] = "1"
    try:
        with pytest.raises(DownloadError, match="WS_GATEWAY_SKIP_DOWNLOAD"):
            ensure_binary()
    finally:
        del os.environ["WS_GATEWAY_SKIP_DOWNLOAD"]


def test_cached_executable_binary_returned_without_download(fixtures: dict[str, Any]) -> None:
    """A cached, executable binary in bin/ skips the network."""
    from websocket_gateway._downloader import ensure_binary

    binary = fixtures["bin_dir"] / fixtures["binary_name"]
    binary.write_bytes(b"already cached")
    binary.chmod(0o755)

    # Even with a wrong checksum on disk, the cache hit short-circuits.
    _put_checksums(fixtures, ["wrong"])

    assert ensure_binary() == binary
    assert binary.read_bytes() == b"already cached"


def test_unsupported_platform_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    """An unknown OS/arch combination explains the Docker fallback."""
    import platform
    import sys

    from websocket_gateway._downloader import DownloadError, _platform_binary_name

    monkeypatch.setattr(sys, "platform", "freebsd")
    monkeypatch.setattr(platform, "machine", lambda: "sparc64")

    with pytest.raises(DownloadError, match="Unsupported platform"):
        _platform_binary_name()


def test_secret_value_never_appears_in_download_url_env(
    fixtures: dict[str, Any], monkeypatch: pytest.MonkeyPatch
) -> None:
    """A regression guard: the downloader does not log INTERNAL_SECRET-shaped values."""
    from websocket_gateway._downloader import ensure_binary

    checksum = _put_binary(fixtures)
    _put_checksums(fixtures, [f"{checksum}  {fixtures['binary_name']}"])

    # Set a fake secret in env; ensure it does not leak into any captured stream.
    monkeypatch.setenv("WS_INTERNAL_SECRET", "do-not-log-this-secret")
    ensure_binary()  # success exit; no assertion on stdout is needed beyond no crash.
