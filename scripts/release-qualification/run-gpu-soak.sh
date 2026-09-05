#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: run-gpu-soak.sh <cuda-image> <models-dir> [cycles]}"
models_dir="${2:?models directory is required}"
cycles="${3:-8}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
artifact_dir="${QUALIFICATION_ARTIFACT_DIR:-$(pwd)/artifacts/release-qualification/gpu}"
gpu_soak_script="${GPU_SOAK_SCRIPT:-$script_dir/gpu-soak.sh}"
monitor_script="${GPU_MONITOR_SCRIPT:-$script_dir/monitor-gpu-soak.sh}"
real_curl="${GPU_REAL_CURL:-$(command -v curl)}"
curl_connect_timeout="${GPU_CURL_CONNECT_TIMEOUT:-10}"
curl_max_time="${GPU_CURL_MAX_TIME:-120}"
inference_curl_max_time="${GPU_INFERENCE_CURL_MAX_TIME:-600}"

# Reject timeout overrides that would disable curl deadlines or fail at runtime.
validate_positive_duration() {
  local name="$1" value="$2"
  if [[ ! "$value" =~ ^[0-9]+([.][0-9]+)?$ ]] \
    || ! awk -v value="$value" 'BEGIN { exit !(value > 0) }'; then
    echo "invalid ${name}: expected a positive numeric duration, got '${value}'" >&2
    return 1
  fi
}

validate_positive_duration GPU_CURL_CONNECT_TIMEOUT "$curl_connect_timeout"
validate_positive_duration GPU_CURL_MAX_TIME "$curl_max_time"
validate_positive_duration GPU_INFERENCE_CURL_MAX_TIME "$inference_curl_max_time"

shim_dir="$(mktemp -d)"
soak_pid=""
monitor_pid=""

# Terminate and reap both child processes so cancellation cannot leave qualification work behind.
cleanup() {
  if [[ -n "$soak_pid" ]] && kill -0 "$soak_pid" >/dev/null 2>&1; then
    kill -TERM "$soak_pid" >/dev/null 2>&1 || true
    wait "$soak_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$monitor_pid" ]] && kill -0 "$monitor_pid" >/dev/null 2>&1; then
    kill -TERM "$monitor_pid" >/dev/null 2>&1 || true
    wait "$monitor_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$shim_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$artifact_dir"
cat >"$shim_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

request_timeout="${GPU_CURL_MAX_TIME:?}"
for arg in "$@"; do
  case "$arg" in
    */api/v1/playground/chat/completions*)
      request_timeout="${GPU_INFERENCE_CURL_MAX_TIME:?}"
      break
      ;;
  esac
done

# Append qualification-owned deadlines last so they override any shorter
# per-call curl defaults inside the soak script.
exec "${LLAMARACK_REAL_CURL:?}" \
  "$@" \
  --connect-timeout "${GPU_CURL_CONNECT_TIMEOUT:?}" \
  --max-time "$request_timeout"
EOF
chmod +x "$shim_dir/curl"

LLAMARACK_REAL_CURL="$real_curl" \
GPU_CURL_CONNECT_TIMEOUT="$curl_connect_timeout" \
GPU_CURL_MAX_TIME="$curl_max_time" \
GPU_INFERENCE_CURL_MAX_TIME="$inference_curl_max_time" \
PATH="$shim_dir:$PATH" \
QUALIFICATION_ARTIFACT_DIR="$artifact_dir" \
  bash "$gpu_soak_script" "$image" "$models_dir" "$cycles" &
soak_pid=$!
container_name="llamarack-gpu-qualification-${soak_pid}"

bash "$monitor_script" "$artifact_dir" "$container_name" &
monitor_pid=$!

set +e
wait "$soak_pid"
soak_status=$?
set -e
soak_pid=""

if kill -0 "$monitor_pid" >/dev/null 2>&1; then
  kill -TERM "$monitor_pid" >/dev/null 2>&1 || true
fi
set +e
wait "$monitor_pid"
monitor_status=$?
set -e
monitor_pid=""

if (( soak_status != 0 )); then
  echo "GPU soak failed with exit status ${soak_status}" >&2
  exit "$soak_status"
fi
if (( monitor_status != 0 )); then
  echo "GPU soak resource monitor failed with exit status ${monitor_status}" >&2
  exit "$monitor_status"
fi

trap - EXIT INT TERM
rm -rf "$shim_dir"
