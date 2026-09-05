#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
wrapper="$repo_root/scripts/release-qualification/run-gpu-soak.sh"
monitor="$repo_root/scripts/release-qualification/monitor-gpu-soak.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Fail the regression harness with a consistent diagnostic.
fail() {
  echo "test-run-gpu-soak: $*" >&2
  exit 1
}

fake_real_curl="$tmpdir/fake-real-curl"
cat >"$fake_real_curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_ROOT:?}/curl-args.txt"
EOF
chmod +x "$fake_real_curl"

fake_soak="$tmpdir/fake-soak.sh"
cat >"$fake_soak" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$$" >"${TEST_ROOT:?}/soak-pid.txt"
curl -sS http://unit.test/health >/dev/null
curl -sS --max-time 120 http://unit.test/api/v1/playground/chat/completions >/dev/null
sleep 0.2
EOF
chmod +x "$fake_soak"

fake_monitor="$tmpdir/fake-monitor.sh"
cat >"$fake_monitor" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$2" >"${TEST_ROOT:?}/monitor-container.txt"
trap 'exit 0' TERM INT
while :; do sleep 0.05; done
EOF
chmod +x "$fake_monitor"

mkdir -p "$tmpdir/models" "$tmpdir/artifacts"
TEST_ROOT="$tmpdir" \
GPU_REAL_CURL="$fake_real_curl" \
GPU_SOAK_SCRIPT="$fake_soak" \
GPU_MONITOR_SCRIPT="$fake_monitor" \
QUALIFICATION_ARTIFACT_DIR="$tmpdir/artifacts" \
  bash "$wrapper" example.invalid/image "$tmpdir/models" 1

soak_pid="$(cat "$tmpdir/soak-pid.txt")"
expected_container="llamarack-gpu-qualification-${soak_pid}"
actual_container="$(cat "$tmpdir/monitor-container.txt")"
[[ "$actual_container" == "$expected_container" ]] \
  || fail "monitor target mismatch: expected $expected_container, got $actual_container"

grep -F -- '-sS http://unit.test/health --connect-timeout 10 --max-time 120' "$tmpdir/curl-args.txt" >/dev/null \
  || fail "GPU soak curl shim did not enforce the control request timeout"
grep -F -- '-sS --max-time 120 http://unit.test/api/v1/playground/chat/completions --connect-timeout 10 --max-time 600' "$tmpdir/curl-args.txt" >/dev/null \
  || fail "GPU soak curl shim did not override inference requests to the longer timeout"

fakebin="$tmpdir/fakebin"
mkdir -p "$fakebin" "$tmpdir/monitor-artifacts"
cat >"$fakebin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_ROOT:?}/docker-args.txt"
case "${1:-}" in
  ps)
    echo "llamarack-gpu-qualification-stale"
    ;;
  inspect)
    if [[ "${2:-}" == "-f" ]]; then
      printf '%s\n' '23456'
      exit 0
    fi
    [[ "${2:-}" == "llamarack-gpu-qualification-target" ]] || exit 1
    ;;
  exec)
    if [[ "$*" == *'VmRSS:'* ]]; then
      printf '%s\n' '2048'
    else
      printf '%s\n' '42'
    fi
    ;;
esac
EOF
chmod +x "$fakebin/docker"

cat >"$fakebin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' 'llamarack_manager_goroutines 7'
EOF
chmod +x "$fakebin/curl"

TEST_ROOT="$tmpdir" PATH="$fakebin:$PATH" \
  bash "$monitor" "$tmpdir/monitor-artifacts" "llamarack-gpu-qualification-target" &
monitor_pid=$!
for _ in $(seq 1 50); do
  if [[ -f "$tmpdir/monitor-artifacts/manager-resource-samples.tsv" ]] \
    && [[ "$(wc -l < "$tmpdir/monitor-artifacts/manager-resource-samples.tsv")" -ge 2 ]]; then
    break
  fi
  sleep 0.05
done
kill -TERM "$monitor_pid"
wait "$monitor_pid"

if grep -q '^ps ' "$tmpdir/docker-args.txt"; then
  fail "monitor enumerated qualification containers instead of using the exact target"
fi
grep -F 'inspect llamarack-gpu-qualification-target' "$tmpdir/docker-args.txt" >/dev/null \
  || fail "monitor did not inspect the exact target container"
[[ "$(wc -l < "$tmpdir/monitor-artifacts/manager-resource-samples.tsv")" -ge 2 ]] \
  || fail "monitor did not record a resource sample"

cancel_root="$tmpdir/cancel"
cancel_bin="$cancel_root/bin"
mkdir -p "$cancel_bin" "$cancel_root/artifacts"
cat >"$cancel_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "inspect" ]]; then
  : >"${TEST_ROOT:?}/docker-inspect-blocked"
  sleep 30
fi
EOF
chmod +x "$cancel_bin/docker"

hanging_soak="$cancel_root/hanging-soak.sh"
cat >"$hanging_soak" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
trap 'exit 0' TERM INT
while :; do sleep 0.05; done
EOF
chmod +x "$hanging_soak"

TEST_ROOT="$cancel_root" \
PATH="$cancel_bin:$PATH" \
GPU_REAL_CURL="$fake_real_curl" \
GPU_SOAK_SCRIPT="$hanging_soak" \
GPU_MONITOR_SCRIPT="$monitor" \
GPU_DOCKER_TIMEOUT="0.5s" \
GPU_DOCKER_KILL_AFTER="0.2s" \
QUALIFICATION_ARTIFACT_DIR="$cancel_root/artifacts" \
  bash "$wrapper" example.invalid/image "$tmpdir/models" 1 &
wrapper_pid=$!
for _ in $(seq 1 100); do
  [[ -f "$cancel_root/docker-inspect-blocked" ]] && break
  sleep 0.02
done
[[ -f "$cancel_root/docker-inspect-blocked" ]] \
  || fail "blocked docker inspect was not exercised"

start_ns="$(python3 -c 'import time; print(time.monotonic_ns())')"
kill -TERM "$wrapper_pid"
set +e
wait "$wrapper_pid"
wrapper_status=$?
set -e
end_ns="$(python3 -c 'import time; print(time.monotonic_ns())')"
elapsed_ms=$(( (end_ns - start_ns) / 1000000 ))
[[ "$wrapper_status" == "143" ]] \
  || fail "cancelled wrapper exited with $wrapper_status instead of 143"
(( elapsed_ms < 1200 )) \
  || fail "wrapper cancellation exceeded Docker timeout bound (${elapsed_ms}ms)"

printf '%s\n' 'test-run-gpu-soak: PASS'
