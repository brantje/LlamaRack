from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))

from common import PROBE_CASE_BY_CONTRACT_ID
CONTRACT = json.loads((ROOT / "contract.json").read_text(encoding="utf-8"))
VERSIONS = json.loads((ROOT / "versions.json").read_text(encoding="utf-8"))


def main() -> None:
    assert CONTRACT["schema_version"] == 1
    assert CONTRACT["release_contract"] == "1.0"
    assert CONTRACT["pinned_clients"] == VERSIONS

    expected_version_keys = {
        "python",
        "node",
        "openai_python",
        "openai_node",
        "litellm",
    }
    assert set(VERSIONS) == expected_version_keys
    for key, value in VERSIONS.items():
        if key in {"python", "node"}:
            assert re.fullmatch(r"\d+(?:\.\d+)?", value), (key, value)
        else:
            assert re.fullmatch(r"\d+\.\d+\.\d+", value), (key, value)

    surfaces = CONTRACT["surfaces"]
    ids = [item["id"] for item in surfaces]
    assert len(ids) == len(set(ids)), "duplicate contract surface id"
    allowed = set(CONTRACT["status_values"])
    assert all(item["status"] in allowed for item in surfaces)

    by_id = {item["id"]: item for item in surfaces}
    required = {
        "models.list",
        "models.retrieve",
        "chat.completions",
        "chat.completions.streaming",
        "responses.create",
        "client_disconnect",
        "lifecycle.ready",
        "lifecycle.cold_autoload",
        "lifecycle.autoload_disabled",
        "lifecycle.concurrent_cold_start",
    }
    assert required.issubset(by_id), required.difference(by_id)
    assert all(by_id[item]["required_probe"] for item in required)
    assert required.issubset(PROBE_CASE_BY_CONTRACT_ID), required.difference(PROBE_CASE_BY_CONTRACT_ID)
    assert set(PROBE_CASE_BY_CONTRACT_ID) == required

    conditional = [item for item in surfaces if item["status"] == "conditional"]
    assert conditional
    assert all(item.get("capability") for item in conditional)

    error_cases = {item["id"]: item["status"] for item in CONTRACT["required_error_cases"]}
    assert error_cases == {
        "invalid_auth": 401,
        "unknown_model": 404,
        "invalid_request": 400,
        "autoload_disabled": 503,
    }
    assert CONTRACT["forbidden_public_fields"]

    print(f"validated {len(surfaces)} compatibility contract surfaces")


if __name__ == "__main__":
    main()
