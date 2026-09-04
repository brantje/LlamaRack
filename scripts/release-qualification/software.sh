#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
artifact_dir="${1:-$repo_root/artifacts/release-qualification/software}"
mkdir -p "$artifact_dir"

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
artifact_dir="$(cd "$artifact_dir" && pwd)"
cd "$repo_root/backend"

run_group() {
  local name="$1"
  shift
  echo "==> ${name}"
  go test -count=1 "$@" 2>&1 | tee "$artifact_dir/${name}.log"
}

# Run named acceptance scenarios first so the release evidence demonstrates the
# issue #120 invariants directly instead of requiring reviewers to infer them
# from a broad package pass.
run_group upgrade-acceptance \
  -run 'Test(FreshDatabaseMigratesToLatestSchema|ReopenAtLatestVersionIsIdempotent|SecondMigrationUpgradesFromBaseline|FailingMigrationRollsBackAndRetries)$' \
  ./internal/database
run_group recovery-acceptance \
  -run 'Test(ReconcileRestartsSurvivingOwnedWorker|ReconcileTerminatesOwnedOrphans|ReconcileNeverKillsUnrelatedOrUnprovenProcesses|ReconcileRejectsPIDReuseAndGenerationMismatch|ReconcileCleansDeadPIDMetadata)$' \
  ./internal/supervisor
run_group download-acceptance \
  -run 'Test(ResumeUsesRangeForMatchingETag|ChangedETagRestartsInsteadOfAppending|DownloadFailureLeavesPartialUnpromoted|DiskFullWriteFailureLeavesDownloadRecoverable)$' \
  ./internal/downloads
run_group filesystem-acceptance \
  -run 'Test(ResolveArtifactFileSafetyBranches|DeleteFilesPlanAndReferenceEdgeCases)$' \
  ./internal/models
run_group lifecycle-acceptance \
  -run 'Test(IdleUnloadStopsInactiveModelButNotActiveRequest|EvictionPlanUsesActivityAlwaysOnAndInstancePolicy|ConcurrentAcquiresCannotBypassDrainClaim|AcquireCancelledWhileWaitingForDrain|MissingGGUFStartFailureIsRecoverable)$' \
  ./internal/lifecycle

# Keep broad package qualification as a second layer. This catches regressions
# in adjacent branches while the named groups above make the release contract
# and failure evidence explicit.
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
        "acceptance_groups": [
            "upgrade-acceptance",
            "recovery-acceptance",
            "download-acceptance",
            "filesystem-acceptance",
            "lifecycle-acceptance"
        ],
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
