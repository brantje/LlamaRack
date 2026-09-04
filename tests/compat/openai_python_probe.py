from __future__ import annotations

import io
import json
import os
import struct
import sys
import wave
from pathlib import Path
from typing import Any

from openai import APIStatusError, OpenAI

from common import (
    assert_no_forbidden_fields,
    base_url,
    fail_if_needed,
    require_capability_env,
    required_env,
    run_case,
    safe_text,
    versions,
    write_evidence,
)


ROOT = Path(__file__).resolve().parent
CONTRACT = json.loads((ROOT / "contract.json").read_text(encoding="utf-8"))
FORBIDDEN = set(CONTRACT["forbidden_public_fields"])
TINY_PNG_DATA_URL = (
    "data:image/png;base64,"
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
)


def status_code(exc: Exception) -> int | None:
    return getattr(exc, "status_code", None)


def expect_status(fn: Any, expected: int) -> dict[str, Any]:
    try:
        fn()
    except APIStatusError as exc:
        actual = status_code(exc)
        if actual != expected:
            raise AssertionError(f"expected HTTP {expected}, got {actual}: {safe_text(exc)}") from exc
        return {"status": actual}
    raise AssertionError(f"expected HTTP {expected}, request succeeded")


def silent_wav() -> bytes:
    buffer = io.BytesIO()
    with wave.open(buffer, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(16000)
        wav.writeframes(struct.pack("<h", 0) * 1600)
    return buffer.getvalue()


def main() -> None:
    pinned = versions()
    import openai

    if openai.__version__ != pinned["openai_python"]:
        raise SystemExit(
            f"OpenAI Python version mismatch: installed={openai.__version__} pinned={pinned['openai_python']}"
        )

    api_key = required_env("LLAMARACK_API_KEY")
    chat_model = required_env("LLAMARACK_CHAT_MODEL")
    responses_model = os.environ.get("LLAMARACK_RESPONSES_MODEL", chat_model).strip() or chat_model
    client = OpenAI(api_key=api_key, base_url=base_url(), timeout=90.0, max_retries=0)
    results: list[dict[str, Any]] = []
    listed_ids: list[str] = []

    def models_list() -> dict[str, Any]:
        nonlocal listed_ids
        page = client.models.list()
        dumped = page.model_dump()
        assert_no_forbidden_fields(dumped, FORBIDDEN)
        listed_ids = [item.id for item in page.data]
        if chat_model not in listed_ids:
            raise AssertionError(f"chat fixture {chat_model!r} missing from /v1/models: {listed_ids}")
        if len(listed_ids) != len(set(listed_ids)):
            raise AssertionError("/v1/models contains duplicate model IDs")
        return {"ids": listed_ids, "object": dumped.get("object")}

    run_case(results, "models.list", models_list)

    def model_retrieve() -> dict[str, Any]:
        item = client.models.retrieve(chat_model)
        dumped = item.model_dump()
        assert_no_forbidden_fields(dumped, FORBIDDEN)
        if item.id != chat_model:
            raise AssertionError(f"retrieve returned model id {item.id!r}, expected {chat_model!r}")
        return dumped

    run_case(results, "models.retrieve", model_retrieve)

    def chat_basic() -> dict[str, Any]:
        response = client.chat.completions.create(
            model=chat_model,
            messages=[{"role": "user", "content": "Reply with the single word OK."}],
            max_tokens=16,
            temperature=0,
        )
        if response.model != chat_model:
            raise AssertionError(f"response model={response.model!r}, expected public Instance ID {chat_model!r}")
        if not response.choices:
            raise AssertionError("chat response has no choices")
        return {
            "id": response.id,
            "model": response.model,
            "finish_reason": response.choices[0].finish_reason,
            "has_usage": response.usage is not None,
        }

    run_case(results, "chat.basic", chat_basic)

    def chat_stream() -> dict[str, Any]:
        stream = client.chat.completions.create(
            model=chat_model,
            messages=[{"role": "user", "content": "Count from one to three using words only."}],
            max_tokens=32,
            temperature=0,
            stream=True,
        )
        chunk_count = 0
        finish_reasons: list[str] = []
        saw_content = False
        for chunk in stream:
            chunk_count += 1
            if chunk.model and chunk.model != chat_model:
                raise AssertionError(f"stream model={chunk.model!r}, expected {chat_model!r}")
            for choice in chunk.choices:
                if choice.delta and choice.delta.content:
                    saw_content = True
                if choice.finish_reason:
                    finish_reasons.append(choice.finish_reason)
        if chunk_count == 0:
            raise AssertionError("stream produced no SDK chunks")
        if not saw_content:
            raise AssertionError("stream produced no content deltas")
        return {"chunks": chunk_count, "finish_reasons": finish_reasons}

    run_case(results, "chat.streaming_sdk", chat_stream)

    def responses_basic() -> dict[str, Any]:
        response = client.responses.create(
            model=responses_model,
            input="Reply with the single word OK.",
            max_output_tokens=16,
        )
        if getattr(response, "model", None) not in (None, responses_model):
            raise AssertionError(
                f"Responses model={getattr(response, 'model', None)!r}, expected {responses_model!r}"
            )
        if not response.id:
            raise AssertionError("Responses result has no id")
        status = getattr(response, "status", None)
        if status != "completed":
            raise AssertionError(f"Responses status={status!r}, expected 'completed'")
        output = getattr(response, "output", None)
        if not output:
            raise AssertionError("Responses result has empty output")
        return {
            "id": response.id,
            "model": getattr(response, "model", None),
            "status": status,
            "output_items": len(output),
        }

    run_case(results, "responses.basic", responses_basic)

    run_case(
        results,
        "errors.invalid_auth",
        lambda: expect_status(
            lambda: OpenAI(
                api_key="sk-llamarack-compat-invalid",
                base_url=base_url(),
                timeout=30.0,
                max_retries=0,
            ).models.list(),
            401,
        ),
    )

    run_case(
        results,
        "errors.unknown_model",
        lambda: expect_status(
            lambda: client.chat.completions.create(
                model="__llamarack_compat_missing_instance__",
                messages=[{"role": "user", "content": "test"}],
                max_tokens=1,
            ),
            404,
        ),
    )

    run_case(
        results,
        "errors.invalid_request",
        lambda: expect_status(
            lambda: client.chat.completions.create(
                model="",
                messages=[{"role": "user", "content": "test"}],
                max_tokens=1,
            ),
            400,
        ),
    )

    def completion_probe() -> dict[str, Any]:
        model = require_capability_env("completion", "LLAMARACK_COMPLETION_MODEL")
        response = client.completions.create(model=model, prompt="Say OK", max_tokens=8, temperature=0)
        if not response.choices:
            raise AssertionError("completion response has no choices")
        return {"model": response.model, "finish_reason": response.choices[0].finish_reason}

    run_case(results, "completions.basic", completion_probe)

    def embeddings_probe() -> dict[str, Any]:
        model = require_capability_env("embeddings", "LLAMARACK_EMBEDDING_MODEL")
        response = client.embeddings.create(model=model, input="LlamaRack compatibility probe")
        if not response.data or not response.data[0].embedding:
            raise AssertionError("embedding response contains no vector")
        return {"model": response.model, "dimensions": len(response.data[0].embedding)}

    run_case(results, "embeddings.basic", embeddings_probe)

    def tools_probe() -> dict[str, Any]:
        model = require_capability_env("tools", "LLAMARACK_TOOLS_MODEL")
        tools = [
            {
                "type": "function",
                "function": {
                    "name": "compat_echo",
                    "description": "Echo an integer exactly.",
                    "parameters": {
                        "type": "object",
                        "properties": {"value": {"type": "integer"}},
                        "required": ["value"],
                        "additionalProperties": False,
                    },
                },
            }
        ]
        first = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": "Call compat_echo with value 7."}],
            tools=tools,
            tool_choice={"type": "function", "function": {"name": "compat_echo"}},
            max_tokens=128,
            temperature=0,
        )
        message = first.choices[0].message
        if not message.tool_calls:
            raise AssertionError("tool-capable fixture returned no tool_calls")
        call = message.tool_calls[0]
        args = json.loads(call.function.arguments)
        if args.get("value") != 7:
            raise AssertionError(f"unexpected tool arguments: {args}")
        second = client.chat.completions.create(
            model=model,
            messages=[
                {"role": "user", "content": "Call compat_echo with value 7."},
                message.model_dump(exclude_none=True),
                {"role": "tool", "tool_call_id": call.id, "content": "7"},
            ],
            tools=tools,
            max_tokens=64,
            temperature=0,
        )
        if not second.choices:
            raise AssertionError("tool round-trip returned no choices")
        return {"tool_call_id": call.id, "arguments": args, "round_trip": True}

    run_case(results, "tools.round_trip", tools_probe)

    def structured_probe() -> dict[str, Any]:
        model = require_capability_env("structured_output", "LLAMARACK_STRUCTURED_MODEL")
        response = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": "Return an object with ok=true and no other fields."}],
            response_format={
                "type": "json_schema",
                "json_schema": {
                    "name": "compat_result",
                    "strict": True,
                    "schema": {
                        "type": "object",
                        "properties": {"ok": {"type": "boolean"}},
                        "required": ["ok"],
                        "additionalProperties": False,
                    },
                },
            },
            max_tokens=64,
            temperature=0,
        )
        content = response.choices[0].message.content or ""
        parsed = json.loads(content)
        if parsed != {"ok": True}:
            raise AssertionError(f"structured response did not match schema expectation: {parsed}")
        return parsed

    run_case(results, "structured_output.json_schema", structured_probe)

    def vision_probe() -> dict[str, Any]:
        model = require_capability_env("vision", "LLAMARACK_VISION_MODEL")
        response = client.chat.completions.create(
            model=model,
            messages=[
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "Describe this image in one short phrase."},
                        {"type": "image_url", "image_url": {"url": TINY_PNG_DATA_URL}},
                    ],
                }
            ],
            max_tokens=32,
        )
        if not response.choices:
            raise AssertionError("vision response has no choices")
        return {"model": response.model, "has_content": bool(response.choices[0].message.content)}

    run_case(results, "multimodal.image", vision_probe)

    def transcription_probe() -> dict[str, Any]:
        model = require_capability_env("transcription", "LLAMARACK_TRANSCRIPTION_MODEL")
        audio = io.BytesIO(silent_wav())
        audio.name = "compat.wav"
        response = client.audio.transcriptions.create(model=model, file=audio)
        return {"text_type": type(response.text).__name__, "length": len(response.text or "")}

    run_case(results, "audio.transcription", transcription_probe)

    evidence = write_evidence(
        "openai-python",
        results,
        {
            "installed_openai": openai.__version__,
            "python": sys.version.split()[0],
            "fixtures": {
                "chat": chat_model,
                "responses": responses_model,
            },
        },
    )
    print(evidence)
    fail_if_needed(results)


if __name__ == "__main__":
    main()
