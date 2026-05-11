"""Tests for the ``runwsgateway`` Django management command."""

from __future__ import annotations

import io
from pathlib import Path
from typing import Any

import pytest
from django.core.management import call_command


@pytest.fixture
def patched_execvpe(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """Capture os.execvpe calls instead of replacing the process.

    Returns a dict that gains keys after the command runs:
        - file: the binary path passed as the first argument.
        - argv: the argv list passed to execvpe.
        - env:  the environment dict passed to execvpe.
    """
    captured: dict[str, Any] = {}
    from websocket_gateway.management.commands import runwsgateway as cmd

    def fake_execvpe(file: str, argv: list[str], env: dict[str, str]) -> None:
        captured["file"] = file
        captured["argv"] = argv
        captured["env"] = env

    monkeypatch.setattr(cmd.os, "execvpe", fake_execvpe)
    return captured


@pytest.fixture
def patched_ensure_binary(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Path:
    """Skip the actual download path by returning a tmp file."""
    from websocket_gateway.management.commands import runwsgateway as cmd

    binary = tmp_path / "gateway"
    binary.write_bytes(b"#!/bin/sh\nexit 0\n")
    binary.chmod(0o755)
    monkeypatch.setattr(cmd, "ensure_binary", lambda: binary)
    return binary


def test_runwsgateway_invokes_execvpe_with_binary(
    apply_settings: dict,
    patched_execvpe: dict[str, Any],
    patched_ensure_binary: Path,
) -> None:
    """The command resolves the binary and calls os.execvpe with it."""
    call_command("runwsgateway", stdout=io.StringIO())

    assert patched_execvpe["file"] == str(patched_ensure_binary)
    assert patched_execvpe["argv"] == [str(patched_ensure_binary)]


def test_runwsgateway_translates_required_settings_to_env(
    apply_settings: dict,
    patched_execvpe: dict[str, Any],
    patched_ensure_binary: Path,
) -> None:
    """Required WEBSOCKET_GATEWAY keys are mapped to gateway env-var names."""
    call_command("runwsgateway", stdout=io.StringIO())
    env = patched_execvpe["env"]

    assert env["INTERNAL_AUTH_SECRET"] == apply_settings["INTERNAL_SECRET"]
    assert env["REDIS_URL"] == apply_settings["REDIS_URL"]
    assert env["ALLOWED_ORIGINS"] == ",".join(apply_settings["ALLOWED_ORIGINS"])


def test_runwsgateway_uses_default_django_auth_url(
    apply_settings: dict,
    patched_execvpe: dict[str, Any],
    patched_ensure_binary: Path,
) -> None:
    """When DJANGO_AUTH_URL is unset, the documented default is used."""
    call_command("runwsgateway", stdout=io.StringIO())
    env = patched_execvpe["env"]
    assert env["DJANGO_AUTH_URL"] == "http://django:8000/internal/ws-auth/"


def test_runwsgateway_passes_optional_settings_through(
    apply_settings: dict,
    patched_execvpe: dict[str, Any],
    patched_ensure_binary: Path,
) -> None:
    """Optional keys appear in env only when present in settings."""
    apply_settings["MAX_CONNECTIONS_PER_USER"] = 25
    apply_settings["PING_INTERVAL"] = "45s"

    call_command("runwsgateway", stdout=io.StringIO())
    env = patched_execvpe["env"]

    assert env["MAX_CONNECTIONS_PER_USER"] == "25"
    assert env["PING_INTERVAL"] == "45s"
    assert "PONG_TIMEOUT" not in env  # not set in settings


def test_runwsgateway_stdout_does_not_contain_secret(
    apply_settings: dict,
    patched_execvpe: dict[str, Any],
    patched_ensure_binary: Path,
) -> None:
    """The success message must not echo INTERNAL_SECRET."""
    out = io.StringIO()
    call_command("runwsgateway", stdout=out)
    assert apply_settings["INTERNAL_SECRET"] not in out.getvalue()


def test_runwsgateway_uses_execvpe_not_popen() -> None:
    """Source-level regression guard: must use os.execvpe, never subprocess.Popen."""
    import ast
    import inspect

    from websocket_gateway.management.commands import runwsgateway

    tree = ast.parse(inspect.getsource(runwsgateway))
    calls: list[str] = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
            calls.append(node.func.attr)
    assert "execvpe" in calls, "must use os.execvpe to replace the process"
    assert "Popen" not in calls, "must not use subprocess.Popen"
    # Module-level imports must not pull in subprocess.
    imports = [n.module for n in ast.walk(tree) if isinstance(n, ast.ImportFrom)] + [
        alias.name for n in ast.walk(tree) if isinstance(n, ast.Import) for alias in n.names
    ]
    assert "subprocess" not in imports
