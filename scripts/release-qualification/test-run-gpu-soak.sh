#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
wrapper="$repo_root/scripts/release-qualification/run-gpu-soak.sh"
monitor="$repo_root/scripts/release-qualification/monitor-gpu-soak.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

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
curl -sS http://unit.test/stream >/dev/null
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

grep -F -- '--connect-timeout 10 --max-time 120 -sS http://unit.test/stream' "$tmpdir/curl-args.txt" >/dev/null \
  || fail "GPU soak curl shim did not enforce connect/max timeouts"

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

printf '%s\n' 'test-run-gpu-soak: PASS'
