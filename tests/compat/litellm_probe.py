from __future__ import annotations

import importlib.metadata
import json
import os
import time
import uuid
from typing import Any
from urllib.parse import quote

import litellm
from openai import OpenAI

from common import (
    NotApplicable,
    base_url,
    fail_if_needed,
    optional_env,
    raw_json,
    required_env,
    run_case,
    safe_text,
    versions,
    write_evidence,
)


def content_from_choice(choice: Any) -> str:
    message = getattr(choice, "message", None)
    if message is None and isinstance(choice, dict):
        message = choice.get("message")
    content = getattr(message, "content", None)
    if content is None and isinstance(message, dict):
        content = message.get("content")
    return content or ""


def verify_trace(trace_id: str) -> dict[str, Any]:
    mgmt_base = optional_env("LLAMARACK_MANAGEMENT_BASE_URL")
    mgmt_key = optional_env("LLAMARACK_MANAGEMENT_KEY")
    if not mgmt_base or not mgmt_key:
        raise NotApplicable("management API fixture is not configured for trace verification")
    deadline = time.monotonic() + 15.0
    last: Any = None
    while time.monotonic() < deadline:
        status, _, payload = raw_json(
            "GET",
            f"{mgmt_base.rstrip('/')}/api/v1/observability/requests?trace_id={quote(trace_id)}&limit=10",
            token=mgmt_key,
            timeout=15.0,
        )
        if status != 200:
            raise AssertionError(f"trace lookup returned HTTP {status}: {safe_text(payload)}")
        last = payload
        items = payload.get("items", []) if isinstance(payload, dict) else []
        if items:
            if any(item.get("trace_id") != trace_id for item in items if isinstance(item, dict)):
                raise AssertionError(f"trace-filtered observability response contained a different trace: {safe_text(items)}")
            return {"trace_id": trace_id, "records": len(items)}
        time.sleep(0.25)
    raise AssertionError(f"trace {trace_id} was not observable after LiteLLM request: {safe_text(last)}")


def direct_basic() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    trace_id = str(uuid.uuid4())
    response = litellm.completion(
        model=f"openai/{model}",
        messages=[{"role": "user", "content": "Reply with the single word OK."}],
        api_base=base_url(),
        api_key=required_env("LLAMARACK_API_KEY"),
        max_tokens=16,
        temperature=0,
        timeout=90,
        max_retries=0,
        extra_headers={"X-LiteLLM-Trace-ID": trace_id},
    )
    choices = getattr(response, "choices", None) or []
    if not choices:
        raise AssertionError("LiteLLM direct completion returned no choices")
    response_model = getattr(response, "model", None)
    if response_model not in (None, model):
        raise AssertionError(f"LiteLLM direct response model={response_model!r}, expected {model!r}")
    return {
        "model": response_model,
        "has_content": bool(content_from_choice(choices[0])),
        "trace_id": trace_id,
    }


def direct_stream() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    stream = litellm.completion(
        model=f"openai/{model}",
        messages=[{"role": "user", "content": "Count from one to three using words only."}],
        api_base=base_url(),
        api_key=required_env("LLAMARACK_API_KEY"),
        max_tokens=32,
        temperature=0,
        timeout=90,
        max_retries=0,
        stream=True,
    )
    chunks = 0
    saw_delta = False
    for chunk in stream:
        chunks += 1
        choices = getattr(chunk, "choices", None) or []
        for choice in choices:
            delta = getattr(choice, "delta", None)
            if delta is None and isinstance(choice, dict):
                delta = choice.get("delta")
            content = getattr(delta, "content", None)
            if content is None and isinstance(delta, dict):
                content = delta.get("content")
            if content:
                saw_delta = True
    if chunks == 0:
        raise AssertionError("LiteLLM direct stream produced no chunks")
    if not saw_delta:
        raise AssertionError("LiteLLM direct stream produced no content delta")
    return {"chunks": chunks, "saw_content_delta": True}


def direct_trace() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    trace_id = str(uuid.uuid4())
    litellm.completion(
        model=f"openai/{model}",
        messages=[{"role": "user", "content": "Reply with OK."}],
        api_base=base_url(),
        api_key=required_env("LLAMARACK_API_KEY"),
        max_tokens=8,
        timeout=90,
        max_retries=0,
        extra_headers={"X-LiteLLM-Trace-ID": trace_id},
    )
    return verify_trace(trace_id)


def proxy_client() -> OpenAI:
    proxy_url = optional_env("LLAMARACK_LITELLM_PROXY_URL")
    proxy_key = optional_env("LLAMARACK_LITELLM_PROXY_KEY")
    if not proxy_url or not proxy_key:
        raise NotApplicable("LiteLLM Proxy fixture is not running")
    return OpenAI(api_key=proxy_key, base_url=proxy_url.rstrip("/"), timeout=90.0, max_retries=0)


def proxy_basic() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    trace_id = str(uuid.uuid4())
    client = proxy_client()
    response = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Reply with the single word OK."}],
        max_tokens=16,
        temperature=0,
        extra_headers={"X-LiteLLM-Trace-ID": trace_id},
    )
    if not response.choices:
        raise AssertionError("LiteLLM Proxy response has no choices")
    if response.model not in (None, model):
        raise AssertionError(f"LiteLLM Proxy returned model={response.model!r}, expected {model!r}")
    return {"model": response.model, "trace_id": trace_id, "has_content": bool(response.choices[0].message.content)}


def proxy_stream() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    client = proxy_client()
    stream = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Count from one to three using words only."}],
        max_tokens=32,
        stream=True,
    )
    chunks = 0
    saw_delta = False
    for chunk in stream:
        chunks += 1
        for choice in chunk.choices:
            if choice.delta.content:
                saw_delta = True
    if chunks == 0 or not saw_delta:
        raise AssertionError(f"LiteLLM Proxy stream incomplete: chunks={chunks}, saw_delta={saw_delta}")
    return {"chunks": chunks, "saw_content_delta": saw_delta}


def proxy_trace() -> dict[str, Any]:
    model = required_env("LLAMARACK_CHAT_MODEL")
    trace_id = str(uuid.uuid4())
    client = proxy_client()
    client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Reply with OK."}],
        max_tokens=8,
        extra_headers={"X-LiteLLM-Trace-ID": trace_id},
    )
    return verify_trace(trace_id)


def main() -> None:
    installed = importlib.metadata.version("litellm")
    pinned = versions()["litellm"]
    if installed != pinned:
        raise SystemExit(f"LiteLLM version mismatch: installed={installed} pinned={pinned}")

    # Avoid retry/fallback behavior masking an incompatibility with the exact LlamaRack Instance.
    litellm.num_retries = 0
    litellm.suppress_debug_info = True

    results: list[dict[str, Any]] = []
    direct = run_case(results, "litellm.direct.basic", direct_basic)
    run_case(results, "litellm.direct.streaming", direct_stream)
    run_case(results, "litellm.direct.trace_header", direct_trace)
    run_case(results, "litellm.proxy.basic", proxy_basic)
    run_case(results, "litellm.proxy.streaming", proxy_stream)
    run_case(results, "litellm.proxy.trace_header", proxy_trace)

    evidence = write_evidence(
        "litellm",
        results,
        {
            "installed_litellm": installed,
            "direct_model": required_env("LLAMARACK_CHAT_MODEL"),
            "proxy_configured": bool(optional_env("LLAMARACK_LITELLM_PROXY_URL")),
            "direct_summary": direct,
        },
    )
    print(evidence)
    fail_if_needed(results)


if __name__ == "__main__":
    main()
