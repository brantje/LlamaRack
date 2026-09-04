from __future__ import annotations

import json
import os
import re
import traceback
from pathlib import Path
from typing import Any, Callable
from urllib import error as urlerror
from urllib import request as urlrequest


class NotApplicable(RuntimeError):
    pass


def required_env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"missing required environment variable: {name}")
    return value


def optional_env(name: str) -> str | None:
    value = os.environ.get(name, "").strip()
    return value or None


def base_url() -> str:
    return required_env("LLAMARACK_BASE_URL").rstrip("/")


def artifact_dir() -> Path:
    path = Path(os.environ.get("LLAMARACK_ARTIFACT_DIR", "artifacts/compat"))
    path.mkdir(parents=True, exist_ok=True)
    return path


def target_id() -> str:
    return os.environ.get("LLAMARACK_TARGET_ID", "unspecified")


def required_capabilities() -> set[str]:
    raw = os.environ.get("LLAMARACK_REQUIRED_CAPABILITIES", "")
    return {item.strip() for item in raw.split(",") if item.strip()}


def require_capability_env(capability: str, env_name: str) -> str:
    value = optional_env(env_name)
    if value:
        return value
    if capability in required_capabilities():
        raise RuntimeError(
            f"capability {capability!r} is required but fixture {env_name} is missing"
        )
    raise NotApplicable(f"no fixture supplied for capability {capability}")


def versions() -> dict[str, str]:
    path = Path(__file__).with_name("versions.json")
    return json.loads(path.read_text(encoding="utf-8"))


def _secrets() -> list[str]:
    names = (
        "LLAMARACK_API_KEY",
        "LLAMARACK_MANAGEMENT_KEY",
        "LLAMARACK_LITELLM_MASTER_KEY",
    )
    return [os.environ[name] for name in names if os.environ.get(name)]


def safe_text(value: Any, limit: int = 1200) -> str:
    text = str(value)
    for secret in _secrets():
        text = text.replace(secret, "<redacted>")
    text = re.sub(r"Bearer\s+[A-Za-z0-9._~+/=-]+", "Bearer <redacted>", text)
    if len(text) > limit:
        text = text[:limit] + "…"
    return text


def run_case(
    results: list[dict[str, Any]],
    name: str,
    fn: Callable[[], Any],
) -> Any:
    try:
        detail = fn()
        results.append({"name": name, "status": "pass", "detail": _json_safe(detail)})
        return detail
    except NotApplicable as exc:
        results.append({"name": name, "status": "not_applicable", "detail": safe_text(exc)})
        return None
    except Exception as exc:  # noqa: BLE001 - evidence must record every probe failure.
        results.append(
            {
                "name": name,
                "status": "fail",
                "detail": safe_text(exc),
                "exception": exc.__class__.__name__,
                "traceback": safe_text("".join(traceback.format_exception(exc))),
            }
        )
        return None


def _json_safe(value: Any) -> Any:
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, dict):
        return {str(k): _json_safe(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [_json_safe(v) for v in value]
    if hasattr(value, "model_dump"):
        return _json_safe(value.model_dump())
    return safe_text(value)


def write_evidence(name: str, results: list[dict[str, Any]], extra: dict[str, Any] | None = None) -> Path:
    payload: dict[str, Any] = {
        "suite": name,
        "target": target_id(),
        "versions": versions(),
        "required_capabilities": sorted(required_capabilities()),
        "results": results,
    }
    if extra:
        payload.update(_json_safe(extra))
    path = artifact_dir() / f"{name}.json"
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return path


def fail_if_needed(results: list[dict[str, Any]]) -> None:
    failures = [item for item in results if item["status"] == "fail"]
    if failures:
        names = ", ".join(item["name"] for item in failures)
        raise SystemExit(f"compatibility probe failed: {names}")


def raw_json(
    method: str,
    url: str,
    *,
    token: str | None = None,
    body: Any = None,
    headers: dict[str, str] | None = None,
    timeout: float = 30.0,
) -> tuple[int, dict[str, str], Any]:
    request_headers = {"Accept": "application/json"}
    if token:
        request_headers["Authorization"] = f"Bearer {token}"
    if headers:
        request_headers.update(headers)
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        request_headers.setdefault("Content-Type", "application/json")
    req = urlrequest.Request(url, data=data, method=method, headers=request_headers)
    try:
        with urlrequest.urlopen(req, timeout=timeout) as response:
            raw = response.read()
            parsed = json.loads(raw) if raw else None
            return response.status, dict(response.headers.items()), parsed
    except urlerror.HTTPError as exc:
        raw = exc.read()
        parsed: Any
        try:
            parsed = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            parsed = raw.decode("utf-8", errors="replace")
        return exc.code, dict(exc.headers.items()), parsed


def assert_error_envelope(payload: Any) -> dict[str, Any]:
    if not isinstance(payload, dict) or not isinstance(payload.get("error"), dict):
        raise AssertionError(f"expected OpenAI-style error object, got {safe_text(payload)}")
    err = payload["error"]
    for field in ("message", "type", "param", "code"):
        if field not in err:
            raise AssertionError(f"error object missing {field!r}: {safe_text(err)}")
    return err


def assert_no_forbidden_fields(value: Any, forbidden: set[str]) -> None:
    if isinstance(value, dict):
        overlap = forbidden.intersection(value)
        if overlap:
            raise AssertionError(f"public response contains forbidden fields: {sorted(overlap)}")
        for child in value.values():
            assert_no_forbidden_fields(child, forbidden)
    elif isinstance(value, list):
        for child in value:
            assert_no_forbidden_fields(child, forbidden)
