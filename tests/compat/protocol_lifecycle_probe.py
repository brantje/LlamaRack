from __future__ import annotations

import concurrent.futures
import http.client
import json
import os
import time
import uuid
from contextlib import contextmanager
from typing import Any
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

from common import (
    NotApplicable,
    assert_error_envelope,
    assert_no_forbidden_fields,
    base_url,
    fail_if_needed,
    optional_env,
    raw_json,
    required_capabilities,
    required_env,
    run_case,
    safe_text,
    write_evidence,
)


FORBIDDEN = {"worker_url", "worker_host", "worker_port", "pid", "gguf_path", "api_key_hash"}


def public_chat(model: str, *, max_tokens: int = 16, timeout: float = 120.0) -> tuple[int, dict[str, str], Any]:
    return raw_json(
        "POST",
        f"{base_url()}/chat/completions",
        token=required_env("LLAMARACK_API_KEY"),
        body={
            "model": model,
            "messages": [{"role": "user", "content": "Reply with the single word OK."}],
            "max_tokens": max_tokens,
            "temperature": 0,
        },
        timeout=timeout,
    )


def management_settings() -> tuple[str, str, str]:
    base = optional_env("LLAMARACK_MANAGEMENT_BASE_URL")
    key = optional_env("LLAMARACK_MANAGEMENT_KEY")
    model = optional_env("LLAMARACK_LIFECYCLE_MODEL")
    if base and key and model:
        return base.rstrip("/"), key, model
    lifecycle_required = {
        "lifecycle_ready",
        "lifecycle_autoload",
        "lifecycle_no_autoload",
    }.intersection(required_capabilities())
    if lifecycle_required:
        missing = [
            name
            for name, value in (
                ("LLAMARACK_MANAGEMENT_BASE_URL", base),
                ("LLAMARACK_MANAGEMENT_KEY", key),
                ("LLAMARACK_LIFECYCLE_MODEL", model),
            )
            if not value
        ]
        raise RuntimeError(f"required lifecycle fixtures are incomplete: {', '.join(missing)}")
    raise NotApplicable("management/lifecycle fixture is not configured")


def mgmt_json(method: str, path: str, body: Any = None, timeout: float = 60.0) -> tuple[int, Any]:
    mgmt_base, key, _ = management_settings()
    status, _, payload = raw_json(
        method,
        f"{mgmt_base}{path}",
        token=key,
        body=body,
        timeout=timeout,
    )
    return status, payload


def instance_snapshot(instance_id: str) -> tuple[dict[str, Any], dict[str, str], dict[str, Any]]:
    status, instance = mgmt_json("GET", f"/api/v1/instances/{instance_id}")
    if status != 200 or not isinstance(instance, dict):
        raise AssertionError(f"failed to read lifecycle Instance: HTTP {status}: {safe_text(instance)}")
    status, options = mgmt_json("GET", f"/api/v1/instances/{instance_id}/options")
    if status != 200 or not isinstance(options, dict):
        raise AssertionError(f"failed to read lifecycle Instance options: HTTP {status}: {safe_text(options)}")
    status, runtime = mgmt_json("GET", f"/api/v1/instances/{instance_id}/runtime")
    if status != 200 or not isinstance(runtime, dict):
        raise AssertionError(f"failed to read lifecycle runtime: HTTP {status}: {safe_text(runtime)}")
    return instance, {str(k): str(v) for k, v in options.items()}, runtime


def update_payload(instance: dict[str, Any], options: dict[str, str], *, autoload: bool) -> dict[str, Any]:
    return {
        "model_id": instance["model_id"],
        "name": instance["name"],
        "enabled": bool(instance.get("enabled", True)),
        "autoload_enabled": autoload,
        "always_on": bool(instance.get("always_on", False)),
        "priority": instance.get("priority") or "normal",
        "eviction_enabled": bool(instance.get("eviction_enabled", True)),
        "idle_unload_seconds": int(instance.get("idle_unload_seconds") or 0),
        "max_pending_requests": int(instance.get("max_pending_requests") or 0),
        "gpu_mode": instance.get("gpu_mode") or "auto",
        "gpu_devices": list(instance.get("gpu_devices") or []),
        "tensor_split": instance.get("tensor_split") or "",
        "request_log_mode": instance.get("request_log_mode") or "metadata",
        "options": options,
    }


def runtime(instance_id: str) -> dict[str, Any]:
    status, payload = mgmt_json("GET", f"/api/v1/instances/{instance_id}/runtime")
    if status != 200 or not isinstance(payload, dict):
        raise AssertionError(f"runtime request failed: HTTP {status}: {safe_text(payload)}")
    return payload


def wait_state(instance_id: str, states: set[str], timeout: float = 120.0) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    last: dict[str, Any] = {}
    while time.monotonic() < deadline:
        last = runtime(instance_id)
        if str(last.get("state")) in states:
            return last
        time.sleep(0.25)
    raise AssertionError(f"Instance {instance_id} did not reach {sorted(states)}; last={safe_text(last)}")


def stop_instance(instance_id: str) -> None:
    status, payload = mgmt_json("POST", f"/api/v1/instances/{instance_id}/stop")
    if status not in (204, 200):
        raise AssertionError(f"stop failed: HTTP {status}: {safe_text(payload)}")
    wait_state(instance_id, {"UNLOADED"}, timeout=90.0)


def start_instance(instance_id: str) -> dict[str, Any]:
    status, payload = mgmt_json("POST", f"/api/v1/instances/{instance_id}/start", timeout=120.0)
    if status not in (200, 202):
        raise AssertionError(f"start failed: HTTP {status}: {safe_text(payload)}")
    return wait_state(instance_id, {"READY"}, timeout=120.0)


def set_autoload(instance_id: str, instance: dict[str, Any], options: dict[str, str], enabled: bool) -> None:
    status, payload = mgmt_json(
        "PUT",
        f"/api/v1/instances/{instance_id}",
        update_payload(instance, options, autoload=enabled),
    )
    if status != 200:
        raise AssertionError(f"failed to set autoload={enabled}: HTTP {status}: {safe_text(payload)}")


@contextmanager
def lifecycle_fixture():
    _, _, instance_id = management_settings()
    instance, options, original_runtime = instance_snapshot(instance_id)
    original_state = str(original_runtime.get("state", "UNLOADED"))
    original_autoload = bool(instance.get("autoload_enabled", True))
    try:
        yield instance_id, instance, options
    finally:
        try:
            current = runtime(instance_id)
            if str(current.get("state")) not in {"UNLOADED", "FAILED"}:
                stop_instance(instance_id)
            set_autoload(instance_id, instance, options, original_autoload)
            if original_state == "READY":
                start_instance(instance_id)
        except Exception as exc:  # noqa: BLE001 - restoration failure must be visible but cannot mask evidence.
            print(f"warning: lifecycle fixture restoration failed: {safe_text(exc)}")


def raw_sse_probe(model: str) -> dict[str, Any]:
    trace_id = str(uuid.uuid4())
    session_id = str(uuid.uuid4())
    body = json.dumps(
        {
            "model": model,
            "messages": [{"role": "user", "content": "Count from one to three using words only."}],
            "max_tokens": 32,
            "temperature": 0,
            "stream": True,
        }
    ).encode("utf-8")
    req = Request(
        f"{base_url()}/chat/completions",
        data=body,
        method="POST",
        headers={
            "Authorization": f"Bearer {required_env('LLAMARACK_API_KEY')}",
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
            "X-LiteLLM-Trace-ID": trace_id,
            "X-LiteLLM-Session-ID": session_id,
        },
    )
    with urlopen(req, timeout=120.0) as response:
        content_type = response.headers.get("Content-Type", "")
        if "text/event-stream" not in content_type.lower():
            raise AssertionError(f"expected SSE content type, got {content_type!r}")
        request_id = response.headers.get("X-LlamaRack-Request-ID", "").strip()
        returned_trace = response.headers.get("X-LiteLLM-Trace-ID", "").strip()
        if not request_id:
            raise AssertionError("streaming response is missing X-LlamaRack-Request-ID")
        if returned_trace != trace_id:
            raise AssertionError(f"trace header mismatch: expected {trace_id}, got {returned_trace!r}")

        data_events: list[dict[str, Any]] = []
        saw_done = False
        saw_content = False
        for raw_line in response:
            line = raw_line.decode("utf-8", errors="strict").rstrip("\r\n")
            if not line or line.startswith(":") or line.startswith("event:") or line.startswith("id:"):
                continue
            if not line.startswith("data:"):
                raise AssertionError(f"invalid SSE field from chat stream: {line!r}")
            payload = line[5:].lstrip()
            if payload == "[DONE]":
                saw_done = True
                continue
            try:
                event = json.loads(payload)
            except json.JSONDecodeError as exc:
                raise AssertionError(f"non-JSON SSE data payload: {payload!r}") from exc
            data_events.append(event)
            assert_no_forbidden_fields(event, FORBIDDEN)
            if event.get("model") not in (None, model):
                raise AssertionError(f"stream leaked/returned non-public model identity: {event.get('model')!r}")
            for choice in event.get("choices") or []:
                delta = choice.get("delta") or {}
                if delta.get("content"):
                    saw_content = True
        if not data_events:
            raise AssertionError("SSE stream contained no JSON data events")
        if not saw_content:
            raise AssertionError("SSE stream contained no content delta")
        if not saw_done:
            raise AssertionError("SSE stream did not terminate with data: [DONE]")
        return {
            "content_type": content_type,
            "request_id_present": True,
            "trace_preserved": True,
            "events": len(data_events),
            "terminal": "[DONE]",
        }


def disconnect_probe(model: str) -> dict[str, Any]:
    parsed = urlsplit(base_url())
    if parsed.scheme not in {"http", "https"}:
        raise AssertionError(f"unsupported base URL scheme for disconnect probe: {parsed.scheme}")
    conn_type = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
    host = parsed.hostname or ""
    port = parsed.port
    conn = conn_type(host, port=port, timeout=30.0)
    path_prefix = parsed.path.rstrip("/")
    payload = json.dumps(
        {
            "model": model,
            "messages": [{"role": "user", "content": "Write a long numbered list of short words."}],
            "max_tokens": 1024,
            "stream": True,
        }
    )
    conn.request(
        "POST",
        f"{path_prefix}/chat/completions",
        body=payload,
        headers={
            "Authorization": f"Bearer {required_env('LLAMARACK_API_KEY')}",
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
        },
    )
    response = conn.getresponse()
    if response.status != 200:
        preview = response.read(1024).decode("utf-8", errors="replace")
        conn.close()
        raise AssertionError(f"disconnect stream did not start successfully: HTTP {response.status}: {safe_text(preview)}")
    first = response.read(1)
    if not first:
        conn.close()
        raise AssertionError("disconnect stream ended before any body byte was received")
    conn.close()
    time.sleep(0.25)
    status, _, follow_up = public_chat(model, timeout=120.0)
    if status != 200:
        raise AssertionError(f"manager was unusable after disconnect: HTTP {status}: {safe_text(follow_up)}")
    return {"stream_started": True, "connection_closed": True, "follow_up_status": status}


def lifecycle_ready_case() -> dict[str, Any]:
    with lifecycle_fixture() as (instance_id, instance, options):
        current = runtime(instance_id)
        if current.get("state") != "READY":
            if current.get("state") not in {"UNLOADED", "FAILED"}:
                stop_instance(instance_id)
            start_instance(instance_id)
        before = runtime(instance_id)
        status, _, payload = public_chat(instance_id)
        if status != 200:
            raise AssertionError(f"READY Instance failed public inference: HTTP {status}: {safe_text(payload)}")
        after = runtime(instance_id)
        if after.get("state") != "READY":
            raise AssertionError(f"READY request changed runtime unexpectedly: {safe_text(after)}")
        if before.get("pid") and after.get("pid") != before.get("pid"):
            raise AssertionError("READY request unexpectedly replaced the worker process")
        return {"status": status, "pid_stable": before.get("pid") == after.get("pid")}


def lifecycle_cold_case() -> dict[str, Any]:
    with lifecycle_fixture() as (instance_id, instance, options):
        current = runtime(instance_id)
        if current.get("state") not in {"UNLOADED", "FAILED"}:
            stop_instance(instance_id)
        set_autoload(instance_id, instance, options, True)
        status, _, payload = public_chat(instance_id, timeout=180.0)
        if status != 200:
            raise AssertionError(f"autoload request failed: HTTP {status}: {safe_text(payload)}")
        ready = wait_state(instance_id, {"READY"}, timeout=120.0)
        if not ready.get("pid"):
            raise AssertionError(f"autoloaded runtime has no worker pid: {safe_text(ready)}")
        return {"status": status, "state": ready.get("state"), "worker_present": True}


def lifecycle_no_autoload_case() -> dict[str, Any]:
    with lifecycle_fixture() as (instance_id, instance, options):
        current = runtime(instance_id)
        if current.get("state") not in {"UNLOADED", "FAILED"}:
            stop_instance(instance_id)
        set_autoload(instance_id, instance, options, False)
        status, _, payload = public_chat(instance_id)
        if status != 503:
            raise AssertionError(f"autoload-disabled request returned HTTP {status}, expected 503: {safe_text(payload)}")
        err = assert_error_envelope(payload)
        after = wait_state(instance_id, {"UNLOADED"}, timeout=10.0)
        if after.get("pid"):
            raise AssertionError(f"autoload-disabled request started a worker: {safe_text(after)}")
        return {"status": status, "code": err.get("code"), "state": after.get("state")}


def lifecycle_concurrent_case() -> dict[str, Any]:
    with lifecycle_fixture() as (instance_id, instance, options):
        current = runtime(instance_id)
        if current.get("state") not in {"UNLOADED", "FAILED"}:
            stop_instance(instance_id)
        set_autoload(instance_id, instance, options, True)
        workers = 4
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
            futures = [executor.submit(public_chat, instance_id, max_tokens=8, timeout=180.0) for _ in range(workers)]
            responses = [future.result(timeout=200.0) for future in futures]
        statuses = [item[0] for item in responses]
        if any(status != 200 for status in statuses):
            raise AssertionError(f"concurrent cold requests did not all succeed: {statuses}")
        ready = wait_state(instance_id, {"READY"}, timeout=120.0)
        pid = ready.get("pid")
        if not pid:
            raise AssertionError(f"concurrent cold start did not converge on a READY worker: {safe_text(ready)}")
        time.sleep(0.25)
        stable = runtime(instance_id)
        if stable.get("pid") != pid or stable.get("state") != "READY":
            raise AssertionError(f"cold requests did not converge on one stable runtime: {safe_text(stable)}")
        return {"requests": workers, "statuses": statuses, "single_stable_runtime": True}


def failed_start_case() -> dict[str, Any]:
    model = optional_env("LLAMARACK_FAILED_START_MODEL")
    if not model:
        if "lifecycle_failed_start" in required_capabilities():
            raise RuntimeError("lifecycle_failed_start is required but LLAMARACK_FAILED_START_MODEL is missing")
        raise NotApplicable("no failed-start lifecycle fixture supplied")
    # The failed-start fixture is deliberately not mutated: its committed configuration must already be invalid.
    status, _, payload = public_chat(model, timeout=180.0)
    if status not in {503, 504}:
        raise AssertionError(f"failed-start fixture returned HTTP {status}, expected 503/504: {safe_text(payload)}")
    err = assert_error_envelope(payload)
    return {"status": status, "code": err.get("code"), "useful_error": bool(err.get("message"))}


def main() -> None:
    chat_model = required_env("LLAMARACK_CHAT_MODEL")
    api_key = required_env("LLAMARACK_API_KEY")
    results: list[dict[str, Any]] = []

    def raw_models() -> dict[str, Any]:
        status, headers, payload = raw_json("GET", f"{base_url()}/models", token=api_key)
        if status != 200:
            raise AssertionError(f"/v1/models returned HTTP {status}: {safe_text(payload)}")
        if "application/json" not in headers.get("Content-Type", "").lower():
            raise AssertionError(f"/v1/models content type is not JSON: {headers.get('Content-Type')!r}")
        assert_no_forbidden_fields(payload, FORBIDDEN)
        ids = [item.get("id") for item in payload.get("data", [])]
        if chat_model not in ids:
            raise AssertionError(f"chat fixture {chat_model!r} missing from raw model list")
        return {"status": status, "content_type": headers.get("Content-Type"), "ids": ids}

    run_case(results, "wire.models", raw_models)

    def raw_invalid_auth() -> dict[str, Any]:
        status, _, payload = raw_json("GET", f"{base_url()}/models", token="sk-llamarack-compat-invalid")
        if status != 401:
            raise AssertionError(f"invalid auth returned HTTP {status}, expected 401")
        err = assert_error_envelope(payload)
        return {"status": status, "type": err.get("type"), "code": err.get("code")}

    run_case(results, "wire.error.invalid_auth", raw_invalid_auth)

    def raw_invalid_request() -> dict[str, Any]:
        status, _, payload = raw_json(
            "POST",
            f"{base_url()}/chat/completions",
            token=api_key,
            body={"messages": [{"role": "user", "content": "test"}]},
        )
        if status != 400:
            raise AssertionError(f"missing-model request returned HTTP {status}, expected 400: {safe_text(payload)}")
        err = assert_error_envelope(payload)
        return {"status": status, "type": err.get("type"), "code": err.get("code")}

    run_case(results, "wire.error.invalid_request", raw_invalid_request)

    def raw_unknown_model() -> dict[str, Any]:
        status, _, payload = public_chat("__llamarack_compat_missing_instance__")
        if status != 404:
            raise AssertionError(f"unknown model returned HTTP {status}, expected 404: {safe_text(payload)}")
        err = assert_error_envelope(payload)
        return {"status": status, "type": err.get("type"), "code": err.get("code")}

    run_case(results, "wire.error.unknown_model", raw_unknown_model)
    run_case(results, "wire.chat.sse", lambda: raw_sse_probe(chat_model))
    run_case(results, "wire.client_disconnect", lambda: disconnect_probe(chat_model))
    run_case(results, "lifecycle.ready", lifecycle_ready_case)
    run_case(results, "lifecycle.cold_autoload", lifecycle_cold_case)
    run_case(results, "lifecycle.autoload_disabled", lifecycle_no_autoload_case)
    run_case(results, "lifecycle.concurrent_cold_start", lifecycle_concurrent_case)
    run_case(results, "lifecycle.failed_start", failed_start_case)

    evidence = write_evidence("protocol-lifecycle", results, {"fixtures": {"chat": chat_model}})
    print(evidence)
    fail_if_needed(results)


if __name__ == "__main__":
    main()
