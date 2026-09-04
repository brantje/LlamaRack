from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent
REQUIRED_PROBE_CASES = {
    "models.list",
    "models.retrieve",
    "chat.basic",
    "chat.streaming_sdk",
    "responses.basic",
    "wire.client_disconnect",
    "lifecycle.ready",
    "lifecycle.cold_autoload",
    "lifecycle.autoload_disabled",
    "lifecycle.concurrent_cold_start",
}


def write_evidence(path: Path, suite: str, results: list[dict[str, object]]) -> None:
    path.write_text(
        json.dumps({"suite": suite, "target": "test", "results": results}, indent=2) + "\n",
        encoding="utf-8",
    )


class SummarizeTests(unittest.TestCase):
    def run_summarize(self, artifact_dir: Path, **env: str) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment["LLAMARACK_ARTIFACT_DIR"] = str(artifact_dir)
        environment["LLAMARACK_REQUIRE_LITELLM_PROXY"] = "0"
        environment.update(env)
        return subprocess.run(
            [sys.executable, str(ROOT / "summarize.py")],
            cwd=ROOT,
            env=environment,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_passes_when_required_probe_cases_pass(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            artifact_dir = Path(tmp)
            results = [{"name": case, "status": "pass", "detail": {}} for case in REQUIRED_PROBE_CASES]
            write_evidence(artifact_dir / "protocol.json", "protocol", results)

            completed = self.run_summarize(artifact_dir)

            self.assertEqual(completed.returncode, 0, completed.stderr or completed.stdout)

    def test_fails_when_required_probe_case_is_missing(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            artifact_dir = Path(tmp)
            results = [
                {"name": case, "status": "pass", "detail": {}}
                for case in REQUIRED_PROBE_CASES
                if case != "responses.basic"
            ]
            write_evidence(artifact_dir / "protocol.json", "protocol", results)

            completed = self.run_summarize(artifact_dir)

            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("required_probe.responses.create", completed.stderr)

    def test_fails_on_unknown_required_capability(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            artifact_dir = Path(tmp)
            results = [{"name": case, "status": "pass", "detail": {}} for case in REQUIRED_PROBE_CASES]
            write_evidence(artifact_dir / "protocol.json", "protocol", results)

            completed = self.run_summarize(
                artifact_dir,
                LLAMARACK_REQUIRED_CAPABILITIES="lifecycle_ready,not_a_real_capability",
            )

            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("required_capability.not_a_real_capability", completed.stderr)


if __name__ == "__main__":
    unittest.main()
