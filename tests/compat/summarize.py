from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


PROXY_CASES = {
    "litellm.proxy.basic",
    "litellm.proxy.streaming",
    "litellm.proxy.trace_header",
}


def main() -> None:
    artifact_dir = Path(os.environ.get("LLAMARACK_ARTIFACT_DIR", "artifacts/compat"))
    files = sorted(path for path in artifact_dir.glob("*.json") if path.name != "summary.json")
    if not files:
        raise SystemExit(f"no compatibility evidence found in {artifact_dir}")

    suites: list[dict[str, Any]] = []
    all_results: list[dict[str, Any]] = []
    targets: set[str] = set()
    for path in files:
        payload = json.loads(path.read_text(encoding="utf-8"))
        results = payload.get("results")
        if not isinstance(results, list):
            continue
        suite = str(payload.get("suite") or path.stem)
        target = str(payload.get("target") or "unspecified")
        targets.add(target)
        suites.append(
            {
                "suite": suite,
                "file": path.name,
                "pass": sum(item.get("status") == "pass" for item in results),
                "fail": sum(item.get("status") == "fail" for item in results),
                "not_applicable": sum(item.get("status") == "not_applicable" for item in results),
            }
        )
        for item in results:
            all_results.append({"suite": suite, **item})

    if len(targets) > 1:
        raise SystemExit(f"compatibility evidence mixes target identifiers: {sorted(targets)}")

    failures = [item for item in all_results if item.get("status") == "fail"]
    require_proxy = os.environ.get("LLAMARACK_REQUIRE_LITELLM_PROXY", "1") != "0"
    if require_proxy:
        proxy = {item.get("name"): item.get("status") for item in all_results if item.get("name") in PROXY_CASES}
        missing = sorted(name for name in PROXY_CASES if proxy.get(name) != "pass")
        if missing:
            failures.append(
                {
                    "suite": "summary",
                    "name": "litellm.proxy.required",
                    "status": "fail",
                    "detail": f"required LiteLLM Proxy cases did not pass: {', '.join(missing)}",
                }
            )

    required_caps = {item.strip() for item in os.environ.get("LLAMARACK_REQUIRED_CAPABILITIES", "").split(",") if item.strip()}
    required_case_map = {
        "lifecycle_ready": "lifecycle.ready",
        "lifecycle_autoload": "lifecycle.cold_autoload",
        "lifecycle_no_autoload": "lifecycle.autoload_disabled",
        "lifecycle_failed_start": "lifecycle.failed_start",
        "completion": "completions.basic",
        "embeddings": "embeddings.basic",
        "tools": "tools.round_trip",
        "structured_output": "structured_output.json_schema",
        "vision": "multimodal.image",
        "transcription": "audio.transcription",
    }
    statuses: dict[str, list[str]] = {}
    for item in all_results:
        statuses.setdefault(str(item.get("name")), []).append(str(item.get("status")))
    for capability in sorted(required_caps):
        case = required_case_map.get(capability)
        if case and "pass" not in statuses.get(case, []):
            failures.append(
                {
                    "suite": "summary",
                    "name": f"required_capability.{capability}",
                    "status": "fail",
                    "detail": f"required case {case!r} did not pass",
                }
            )

    summary = {
        "contract": "1.0",
        "target": next(iter(targets), "unspecified"),
        "suites": suites,
        "required_capabilities": sorted(required_caps),
        "require_litellm_proxy": require_proxy,
        "total": {
            "pass": sum(item.get("status") == "pass" for item in all_results),
            "fail": len(failures),
            "not_applicable": sum(item.get("status") == "not_applicable" for item in all_results),
        },
        "failures": failures,
    }
    output = artifact_dir / "summary.json"
    output.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(output)
    print(json.dumps(summary["total"], sort_keys=True))
    if failures:
        names = ", ".join(str(item.get("name")) for item in failures)
        raise SystemExit(f"compatibility qualification failed: {names}")


if __name__ == "__main__":
    main()
