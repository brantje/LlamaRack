#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
artifact_dir="${1:-$repo_root/artifacts/release-qualification/software}"
mkdir -p "$artifact_dir"

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cd "$repo_root/backend"

run_group() {
  local name="$1"
  shift
  echo "==> ${name}"
  go test -count=1 "$@" 2>&1 | tee "$artifact_dir/${name}.log"
}

# These packages are intentionally grouped as the deterministic software part
# of release qualification. They cover migration/upgrade semantics, stale-worker
# recovery, download interruption/resume behavior, model filesystem boundaries,
# and lifecycle/scheduler admission and eviction invariants.
run_group database ./internal/database
run_group recovery ./internal/supervisor
run_group downloads ./internal/downloads
run_group models ./internal/models
run_group lifecycle ./internal/lifecycle

finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python3 - "$artifact_dir/manifest.json" <<PY
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({
        "scenario": "software-release-qualification",
        "commit": os.environ.get("GITHUB_SHA", ""),
        "started_at": "${started_at}",
        "finished_at": "${finished_at}",
        "domains": [
            "database-migrations-and-upgrade",
            "crash-and-stale-worker-recovery",
            "download-interruption-and-resume",
            "model-filesystem-safety",
            "lifecycle-and-scheduler-invariants"
        ],
        "result": "pass"
    }, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

echo "software release qualification passed"
