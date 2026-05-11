"""Fetch and verify the Go gateway binary from GitHub Releases.

On the first invocation of ``python manage.py runwsgateway``, this module
downloads the platform-appropriate binary from the project's GitHub Releases
page, verifies it against the published SHA-256 sum, marks it executable,
and caches it in ``websocket_gateway/bin/``. Subsequent invocations reuse the
cached file.

Three environment variables override the default behaviour:

==================================  ============================================
Variable                            Effect
==================================  ============================================
``WS_GATEWAY_BINARY_PATH``          Skip download entirely and use the binary
                                    at the given path. Useful for local
                                    development.
``WS_GATEWAY_SKIP_DOWNLOAD``        Refuse to download even when nothing is
                                    cached. Combine with the path override or
                                    use the Docker image.
``WS_GATEWAY_DOWNLOAD_URL``         Replace the default GitHub URL prefix.
                                    Used by the test suite to point at a
                                    local HTTP server.
==================================  ============================================

Security properties:

* The SHA-256 sum is fetched first and compared against the downloaded file
  before any bytes are made executable. A mismatch raises
  :class:`DownloadError` and leaves no file at the destination.
* The download lands in a temporary file in the same directory as the final
  destination, so ``shutil.move`` is an atomic rename.
* Execute bits are set only after successful verification.
"""

from __future__ import annotations

import hashlib
import os
import platform
import shutil
import stat
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

from ._version import __version__

GITHUB_REPO = "raghulj/django-websocket-gateway"
BASE_URL_TEMPLATE = "https://github.com/{repo}/releases/download/v{version}"


class DownloadError(RuntimeError):
    """Raised when the binary cannot be obtained or verified."""


def ensure_binary() -> Path:
    """Return a path to a verified, executable gateway binary.

    Resolution order:

    1. If ``WS_GATEWAY_BINARY_PATH`` is set and points at an existing file,
       use it as-is.
    2. If the platform binary already exists under ``bin/`` and is
       executable, use it.
    3. Otherwise download from GitHub Releases (or the URL in
       ``WS_GATEWAY_DOWNLOAD_URL``), verify SHA-256, set execute bits,
       and return the path.

    Returns:
        The absolute path to a usable binary.

    Raises:
        DownloadError: For every failure mode — unsupported platform,
            checksum mismatch, network error, missing checksum entry,
            ``WS_GATEWAY_SKIP_DOWNLOAD`` with no cached binary.
    """
    if override := os.environ.get("WS_GATEWAY_BINARY_PATH"):
        p = Path(override)
        if not p.exists():
            raise DownloadError(f"WS_GATEWAY_BINARY_PATH={override} does not exist.")
        return p

    bin_dir = _bin_dir()
    bin_dir.mkdir(parents=True, exist_ok=True)
    binary_name = _platform_binary_name()
    binary_path = bin_dir / binary_name

    if binary_path.exists() and os.access(binary_path, os.X_OK):
        return binary_path

    if os.environ.get("WS_GATEWAY_SKIP_DOWNLOAD"):
        raise DownloadError(
            f"Binary not found at {binary_path} and WS_GATEWAY_SKIP_DOWNLOAD is set. "
            f"Set WS_GATEWAY_BINARY_PATH, unset the skip flag, or use the Docker image "
            f"ghcr.io/{GITHUB_REPO}:{__version__}."
        )

    _download_and_verify(binary_name, binary_path)
    mode = binary_path.stat().st_mode
    binary_path.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return binary_path


def _bin_dir() -> Path:
    """Return the package-relative ``bin/`` cache directory.

    Defined as a function so tests can monkeypatch it to point at a tmp dir.
    """
    return Path(__file__).resolve().parent / "bin"


def _platform_binary_name() -> str:
    """Return the asset filename for the current OS/arch combination."""
    system_map = {"linux": "linux", "darwin": "darwin"}
    machine_map = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }
    sys_key = system_map.get(sys.platform)
    arch_key = machine_map.get(platform.machine().lower())
    if not sys_key or not arch_key:
        raise DownloadError(
            f"Unsupported platform: {sys.platform}/{platform.machine()}. "
            f"Use the Docker image ghcr.io/{GITHUB_REPO}:{__version__}."
        )
    return f"gateway-{sys_key}-{arch_key}"


def _base_url() -> str:
    if override := os.environ.get("WS_GATEWAY_DOWNLOAD_URL"):
        return override.rstrip("/")
    return BASE_URL_TEMPLATE.format(repo=GITHUB_REPO, version=__version__)


def _download_and_verify(binary_name: str, dest: Path) -> None:
    base = _base_url()
    expected = _fetch_checksum(f"{base}/SHA256SUMS", binary_name)
    tmp_handle = tempfile.NamedTemporaryFile(  # noqa: SIM115 - we close manually
        delete=False, dir=dest.parent, prefix=".dl-"
    )
    tmp_path = Path(tmp_handle.name)
    tmp_handle.close()
    try:
        _stream_download(f"{base}/{binary_name}", tmp_path)
        actual = _sha256(tmp_path)
        if actual != expected:
            raise DownloadError(
                f"checksum mismatch for {binary_name}: expected {expected}, got {actual}."
            )
        shutil.move(str(tmp_path), str(dest))
    finally:
        if tmp_path.exists():
            tmp_path.unlink(missing_ok=True)


def _stream_download(url: str, dest: Path, timeout: int = 120) -> None:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp, dest.open("wb") as out:
            shutil.copyfileobj(resp, out, length=65536)
    except urllib.error.URLError as exc:
        raise DownloadError(f"Download failed: {url}: {exc}") from exc


def _fetch_checksum(url: str, binary_name: str) -> str:
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            content = resp.read().decode()
    except urllib.error.URLError as exc:
        raise DownloadError(f"Checksum fetch failed: {url}: {exc}") from exc
    for line in content.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) >= 2 and parts[1].lstrip("*") == binary_name:
            return parts[0]
    raise DownloadError(f"Checksum for {binary_name} not in SHA256SUMS at {url}")


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()
