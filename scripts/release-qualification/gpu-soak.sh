#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   gpu-soak.sh <cuda-image> <models-dir> [cycles]
#
# models-dir must contain the four role-specific GGUFs (symlinks allowed):
#   qualification-llama-8B.gguf     dense lifecycle + single-GPU pressure
#                                   (or qualification.gguf)
#   qualification-12B.gguf          multi-GPU dense smoke/placement (≥2 GPUs)
#   qualification-moe-4ba1b.gguf    MoE CPU offload (or qualification-moe.gguf)
#   qualification-moe-26b-a4b.gguf  multi-GPU MoE placement (required when ≥2 GPUs)
#
# Optional absolute-path overrides:
#   GPU_QUALIFICATION_DENSE_LIFECYCLE
#   GPU_QUALIFICATION_DENSE_MULTI
#   GPU_QUALIFICATION_MOE_SMALL
#   GPU_QUALIFICATION_MOE_LARGE

image="${1:?usage: gpu-soak.sh <cuda-image> <models-dir> [cycles]}"
models_dir_arg="${2:?models directory is required}"
cycles="${3:-8}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for command in docker curl python3 nvidia-smi; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -d "$models_dir_arg" ]] || { echo "models directory not found: $models_dir_arg" >&2; exit 1; }
models_dir="$(cd "$models_dir_arg" && pwd)"

pick_model() {
  local override="$1"; shift
  local chosen=""
  if [[ -n "$override" ]]; then
    [[ -f "$override" ]] || { echo "qualification model not found: $override" >&2; exit 1; }
    chosen="$override"
  else
    local name
    for name in "$@"; do
      if [[ -f "$models_dir/$name" ]]; then
        chosen="$models_dir/$name"
        break
      fi
    done
    if [[ -z "$chosen" ]]; then
      echo "none of [$*] found under $models_dir" >&2
      exit 1
    fi
  fi
  printf '%s\n' "$(cd "$(dirname "$chosen")" && pwd)/$(basename "$chosen")"
}

extra_volume_args=()
container_model_path() {
  local host="$1" role="$2"
  local dest="/models/qualification/$(basename "$host")"
  container_model_path_result=""
  case "$host" in
    "$models_dir"/*)
      container_model_path_result="$dest"
      return 0
      ;;
  esac
  dest="/models/qualification/${role}-$(basename "$host")"
  extra_volume_args+=(-v "$host:${dest}:ro")
  container_model_path_result="$dest"
}

dense_lifecycle_host="$(pick_model "${GPU_QUALIFICATION_DENSE_LIFECYCLE:-}" qualification-llama-8B.gguf qualification.gguf)"
dense_multi_host="$(pick_model "${GPU_QUALIFICATION_DENSE_MULTI:-}" qualification-12B.gguf)"
moe_small_host="$(pick_model "${GPU_QUALIFICATION_MOE_SMALL:-}" qualification-moe-4ba1b.gguf qualification-moe.gguf)"
moe_large_host="$(pick_model "${GPU_QUALIFICATION_MOE_LARGE:-}" qualification-moe-26b-a4b.gguf)"
container_model_path "$dense_lifecycle_host" lifecycle
dense_lifecycle_path="$container_model_path_result"
container_model_path "$dense_multi_host" multi
dense_multi_path="$container_model_path_result"
container_model_path "$moe_small_host" moe-small
moe_small_path="$container_model_path_result"
container_model_path "$moe_large_host" moe-large
moe_large_path="$container_model_path_result"

artifact_dir="${QUALIFICATION_ARTIFACT_DIR:-$(pwd)/artifacts/release-qualification/gpu}"
mkdir -p "$artifact_dir"
config_dir="$(mktemp -d)"
chmod 0777 "$config_dir"
container_name="llamarack-gpu-qualification-$$"
base_url=""
docker_probe_host="${GPU_DOCKER_HOST:-127.0.0.1}"
docker_publish_host="127.0.0.1"

{
  printf 'models_dir=%s\n' "$models_dir"
  printf 'dense_lifecycle=%s\n' "$dense_lifecycle_host"
  printf 'dense_multi=%s\n' "$dense_multi_host"
  printf 'moe_small=%s\n' "$moe_small_host"
  printf 'moe_large=%s\n' "$moe_large_host"
} | tee "$artifact_dir/model-matrix.txt"

if [[ -f /.dockerenv && "$docker_probe_host" == "127.0.0.1" ]]; then
  docker_probe_host="$(python3 - <<'PY'
import socket
import struct

try:
    socket.gethostbyname("host.docker.internal")
    print("host.docker.internal")
    raise SystemExit(0)
except OSError:
    pass

try:
    with open("/proc/net/route", encoding="utf-8") as routes:
        next(routes, None)
        for line in routes:
            fields = line.split()
            if len(fields) < 4 or fields[1] != "00000000":
                continue
            flags = int(fields[3], 16)
            if not flags & 0x2:
                continue
            gateway = socket.inet_ntoa(struct.pack("<L", int(fields[2], 16)))
            print(gateway)
            raise SystemExit(0)
except (OSError, ValueError) as exc:
    raise SystemExit(f"unable to inspect container default route: {exc}")

raise SystemExit(
    "containerized GPU runner cannot resolve the Docker host; "
    "set GPU_DOCKER_HOST to a Docker-host address reachable from the runner"
)
PY
)"
fi
if [[ "$docker_probe_host" != "127.0.0.1" ]]; then
  docker_publish_host="$(python3 - "$docker_probe_host" <<'PY'
import socket
import sys

try:
    print(socket.gethostbyname(sys.argv[1]))
except OSError as exc:
    raise SystemExit(f"unable to resolve Docker host {sys.argv[1]!r}: {exc}")
PY
)"
fi
printf 'probe_host=%s\npublish_host=%s\n' "$docker_probe_host" "$docker_publish_host" \
  >"$artifact_dir/docker-network.txt"

cleanup() {
  docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
  # llama.cpp may create root-owned slot directories inside the host-backed
  # qualification config directory. Make the dedicated temp tree removable by
  # the non-root Actions runner before destroying the container.
  docker exec -u 0 "$container_name" sh -c 'chmod -R a+rwX /config' >/dev/null 2>&1 || true
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$config_dir"
}
trap cleanup EXIT

nvidia-smi -L | tee "$artifact_dir/nvidia-smi.txt"
nvidia-smi --query-gpu=index,uuid,name,memory.total,driver_version --format=csv,noheader \
  | tee -a "$artifact_dir/nvidia-smi.txt"

docker pull "$image"
docker image inspect "$image" >"$artifact_dir/docker-image.json"
docker run -d --gpus all --name "$container_name" \
  --entrypoint sh \
  -p "${docker_publish_host}::8000" \
  -v "$config_dir:/config" \
  -v "$models_dir:/models/qualification:ro" \
  ${extra_volume_args[@]+"${extra_volume_args[@]}"} \
  "$image" -c 'while :; do sleep 3600; done' >/dev/null
port="$(docker port "$container_name" 8000/tcp | awk -F: 'NR == 1 { print $NF }')"
[[ "$port" =~ ^[0-9]+$ ]] || { echo "unable to resolve Docker-published manager port" >&2; exit 1; }
base_url="http://${docker_probe_host}:${port}"

start_manager() {
  docker exec -d "$container_name" sh -c 'exec /usr/local/bin/llamarack >>/config/manager.log 2>&1'
  for _ in $(seq 1 120); do
    curl -fsS "$base_url/health" >/dev/null 2>&1 && return 0
    sleep 1
  done
  docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
  echo "manager did not become healthy" >&2
  return 1
}

manager_pid() {
  docker exec -u 0 "$container_name" sh -c '
    for p in /proc/[0-9]*; do
      [ "$(cat "$p/comm" 2>/dev/null)" = "llamarack" ] && { echo "${p##*/}"; exit 0; }
    done
    exit 1
  '
}

kill_manager() {
  local pid
  pid="$(manager_pid)"
  docker exec -u 0 "$container_name" kill -9 "$pid"
  for _ in $(seq 1 50); do
    curl -fsS "$base_url/health" >/dev/null 2>&1 || return 0
    sleep 0.1
  done
  echo "manager remained healthy after SIGKILL" >&2
  return 1
}

json_value() {
  local expression="$1"
  python3 -c "import json,sys; data=json.load(sys.stdin); print(${expression})"
}

log_step() {
  printf '\n=== %s  %s ===\n' "$(date -u +%H:%M:%SZ)" "$*"
}

auth_request() {
  local method="$1" path="$2" body="${3:-}" tmp status
  tmp="$(mktemp)"
  if [[ -n "$body" ]]; then
    status="$(curl -sS --connect-timeout 10 --max-time 120 -o "$tmp" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $management_token" \
      -H 'Content-Type: application/json' -d "$body" "$base_url$path")" || true
  else
    status="$(curl -sS --connect-timeout 10 --max-time 120 -o "$tmp" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $management_token" "$base_url$path")" || true
  fi
  if [[ ! "$status" =~ ^2 ]]; then
    echo "request failed: $method $path HTTP ${status}" >&2
    cat "$tmp" >&2 || true
    rm -f "$tmp"
    return 1
  fi
  cat "$tmp"
  rm -f "$tmp"
}

runtime_state() {
  local instance_id="$1"
  auth_request GET "/api/v1/instances/${instance_id}/runtime" | json_value 'data["state"]'
}

wait_state() {
  local instance_id="$1" wanted="$2" i
  printf 'waiting %s -> %s\n' "$instance_id" "$wanted"
  for i in $(seq 1 180); do
    state="$(runtime_state "$instance_id")"
    if [[ "$state" == "$wanted" ]]; then
      printf '  %s reached %s after %ss\n' "$instance_id" "$wanted" "$i"
      return 0
    fi
    [[ "$state" == "FAILED" ]] && {
      echo "instance ${instance_id} FAILED while waiting for ${wanted}" >&2
      auth_request GET "/api/v1/instances/${instance_id}/runtime" >&2 || true
      return 1
    }
    if (( i % 15 == 0 )); then
      printf '  still waiting %s -> %s (%ss, last=%s)\n' "$instance_id" "$wanted" "$i" "$state"
    fi
    sleep 1
  done
  echo "instance ${instance_id} did not reach ${wanted}" >&2
  return 1
}

wait_state_through_failure() {
  local instance_id="$1" wanted="$2"
  for _ in $(seq 1 90); do
    state="$(runtime_state "$instance_id")"
    [[ "$state" == "$wanted" ]] && return 0
    sleep 1
  done
  echo "instance ${instance_id} did not recover to ${wanted}" >&2
  auth_request GET "/api/v1/instances/${instance_id}/runtime" >&2 || true
  return 1
}

wait_process_gone() {
  local pid="$1"
  for _ in $(seq 1 100); do
    if ! docker exec -u 0 "$container_name" sh -c 'kill -0 "$1" 2>/dev/null' sh "$pid"; then
      return 0
    fi
    sleep 0.1
  done
  echo "process ${pid} remained after SIGKILL" >&2
  return 1
}

worker_count() {
  local instance_id="$1"
  # The managed worker runs as the image's USER (1000). Linux ptrace/procfs
  # access rules may deny /proc/<pid>/environ to a root docker-exec process
  # without CAP_SYS_PTRACE, while the same-UID runtime user can read its child.
  docker exec "$container_name" sh -c '
    instance="$1"; count=0
    for env in /proc/[0-9]*/environ; do
      if (tr "\000" "\n" <"$env") 2>/dev/null | grep -qx "LLAMARACK_INSTANCE_ID=$instance"; then
        count=$((count + 1))
      fi
    done
    echo "$count"
  ' sh "$instance_id"
}

assert_single_worker() {
  local instance_id="$1" count
  count="$(worker_count "$instance_id")"
  [[ "$count" == "1" ]] || { echo "worker invariant violated for $instance_id: count=$count" >&2; exit 1; }
}

assert_worker_identity() {
  local pid="$1" instance_id="$2"
  docker exec "$container_name" sh -c '
    pid="$1"; instance="$2"
    (tr "\000" "\n" <"/proc/${pid}/environ") 2>/dev/null \
      | grep -qx "LLAMARACK_INSTANCE_ID=$instance"
  ' sh "$pid" "$instance_id"
}

wait_no_worker() {
  local instance_id="$1"
  for _ in $(seq 1 100); do
    [[ "$(worker_count "$instance_id")" == "0" ]] && return 0
    sleep 0.1
  done
  echo "stale worker remained for $instance_id" >&2
  return 1
}

infer() {
  local instance_id="$1"
  auth_request POST /api/v1/playground/chat/completions \
    "{\"model\":\"$instance_id\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with the word OK.\"}],\"max_tokens\":16,\"temperature\":0,\"stream\":false}" \
    | python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("choices")'
}

stream_infer() {
  local instance_id="$1" output="$2"
  curl -fsS -N -X POST \
    -H "Authorization: Bearer $management_token" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$instance_id\",\"messages\":[{\"role\":\"user\",\"content\":\"Count from one to five.\"}],\"max_tokens\":64,\"stream\":true}" \
    "$base_url/api/v1/playground/chat/completions" >"$output"
  grep -q '^data:' "$output"
}

cancel_stream() {
  local instance_id="$1" output="$2" request_pid
  curl -fsS -N --limit-rate 1k -X POST \
    -H "Authorization: Bearer $management_token" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$instance_id\",\"messages\":[{\"role\":\"user\",\"content\":\"Write a very long numbered list with detailed explanations.\"}],\"max_tokens\":4096,\"stream\":true}" \
    "$base_url/api/v1/playground/chat/completions" >"$output" 2>&1 &
  request_pid=$!
  sleep 0.5
  kill -0 "$request_pid" 2>/dev/null || { echo "stream completed before cancellation could be exercised" >&2; return 1; }
  kill -TERM "$request_pid"
  wait "$request_pid" || true
  sleep 0.5
}

create_model() {
  local name="$1" path="$2" context="${3:-4096}"
  auth_request POST /api/v1/models "{\"name\":\"$name\",\"gguf_path\":\"$path\",\"context_length\":${context}}" \
    | json_value 'data["model"]["id"]'
}

create_instance() {
  local model_id="$1" name="$2" slug="$3" body="$4"
  auth_request POST /api/v1/instances \
    "{\"model_id\":\"$model_id\",\"name\":\"$name\",\"slug\":\"$slug\"${body}}" \
    | json_value 'data["id"]'
}

model_total_bytes() {
  local model_id="$1"
  auth_request GET "/api/v1/models/${model_id}" | python3 -c 'import json,sys; d=json.load(sys.stdin); m=d.get("data", d); print(int(m.get("total_bytes") or 0))'
}

# Quick load + infer + stop for every matrix member so each GGUF is proven on
# this runner before the longer lifecycle / pressure / MoE scenarios.
smoke_model() {
  local label="$1" model_id="$2" slug="$3"
  local extra="${4:-,\"gpu_mode\":\"auto\"}"
  local instance_id
  log_step "smoke ${label}"
  instance_id="$(create_instance "$model_id" "Smoke ${label}" "$slug" "$extra")"
  printf 'starting smoke instance %s (%s)\n' "$instance_id" "$slug"
  auth_request POST "/api/v1/instances/${instance_id}/start" >/dev/null
  wait_state "$instance_id" READY
  assert_single_worker "$instance_id"
  infer "$instance_id"
  auth_request POST "/api/v1/instances/${instance_id}/stop" >/dev/null
  wait_state "$instance_id" UNLOADED
  printf 'smoke %s passed\n' "$label"
}

verify_moe_launch() {
  local instance_id="$1" expected_devices="$2" args_file="$3" environ_file="$4"
  local worker_pid
  worker_pid="$(auth_request GET "/api/v1/instances/${instance_id}/runtime" | json_value 'data["pid"]')"
  assert_worker_identity "$worker_pid" "$instance_id"
  docker exec -u 0 "$container_name" sh -c "tr '\\000' '\\n' </proc/${worker_pid}/cmdline" >"$args_file"
  python3 - "$args_file" "$expected_devices" <<'PY'
import sys
args = [line.rstrip("\n") for line in open(sys.argv[1], encoding="utf-8")]
expected_devices = sys.argv[2]
assert "--n-cpu-moe" in args, args
cpu_moe_index = args.index("--n-cpu-moe")
assert cpu_moe_index + 1 < len(args), args
assert args[cpu_moe_index + 1] == "1", args
index = args.index("--device")
assert index + 1 < len(args), args
assert args[index + 1] == expected_devices, (args, expected_devices)
PY
  docker exec "$container_name" sh -c "(tr '\\000' '\\n' </proc/${worker_pid}/environ) 2>/dev/null" >"$environ_file"
  grep -qx "LLAMARACK_INSTANCE_ID=${instance_id}" "$environ_file"
}

verify_dense_multi_launch() {
  local instance_id="$1" expected_devices="$2" args_file="$3" environ_file="$4"
  local worker_pid
  worker_pid="$(auth_request GET "/api/v1/instances/${instance_id}/runtime" | json_value 'data["pid"]')"
  assert_worker_identity "$worker_pid" "$instance_id"
  docker exec -u 0 "$container_name" sh -c "tr '\\000' '\\n' </proc/${worker_pid}/cmdline" >"$args_file"
  python3 "$script_dir/verify-dense-multi-evidence.py" "$args_file"
  python3 - "$args_file" "$expected_devices" <<'PY'
import sys
args = [line.rstrip("\n") for line in open(sys.argv[1], encoding="utf-8")]
expected_devices = sys.argv[2]
index = args.index("--device")
assert args[index + 1] == expected_devices, (args, expected_devices)
PY
  docker exec "$container_name" sh -c "(tr '\\000' '\\n' </proc/${worker_pid}/environ) 2>/dev/null" >"$environ_file"
  grep -qx "LLAMARACK_INSTANCE_ID=${instance_id}" "$environ_file"
}

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
start_manager

curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"qualification-admin","password":"qualification-password-120"}' \
  "$base_url/api/v1/auth/bootstrap" >/dev/null
login="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"qualification-admin","password":"qualification-password-120"}' \
  "$base_url/api/v1/auth/login")"
management_token="$(printf '%s' "$login" | json_value 'data["access_token"]')"

# Exercise durable settings and service-account creation in the fresh install.
auth_request PUT /api/v1/settings/general '{"idle_unload_seconds":23}' >/dev/null
service_account="$(auth_request POST /api/v1/admin/service-accounts '{"name":"GPU Qualification"}')"
service_account_id="$(printf '%s' "$service_account" | json_value 'data["id"]')"
[[ -n "$service_account_id" ]]

hardware="$(auth_request GET /api/v1/hardware)"
printf '%s\n' "$hardware" >"$artifact_dir/hardware.json"
mapfile -t gpu_ids < <(printf '%s' "$hardware" | python3 -c 'import json,sys; [print(g["id"]) for g in json.load(sys.stdin)["gpus"]]')
[[ ${#gpu_ids[@]} -ge 1 ]] || { echo "release qualification requires at least one visible GPU" >&2; exit 1; }
log_step "register models (${#gpu_ids[@]} GPU(s))"
printf '8B=%s  12B=%s  moe-small=%s  moe-large=%s\n' \
  "$(basename "$dense_lifecycle_host")" \
  "$(basename "$dense_multi_host")" \
  "$(basename "$moe_small_host")" \
  "$(basename "$moe_large_host")"

dense_lifecycle_model_id="$(create_model 'Qualification Dense Lifecycle' "$dense_lifecycle_path")"
dense_multi_model_id="$(create_model 'Qualification Dense Multi' "$dense_multi_path")"
moe_small_model_id="$(create_model 'Qualification MoE Small' "$moe_small_path")"
moe_large_model_id="$(create_model 'Qualification MoE Large' "$moe_large_path")"
pressure_bytes="$(model_total_bytes "$dense_lifecycle_model_id")"
[[ "$pressure_bytes" =~ ^[0-9]+$ && "$pressure_bytes" -gt 0 ]] || {
  echo "dense lifecycle/pressure model total_bytes is missing or zero" >&2
  exit 1
}

smoke_model 'Dense Lifecycle' "$dense_lifecycle_model_id" 'qualification-smoke-dense-lifecycle'
smoke_model 'MoE Small' "$moe_small_model_id" 'qualification-smoke-moe-small'
dense_multi_exercised=0
if (( ${#gpu_ids[@]} >= 2 )); then
  smoke_gpu_json="$(printf '%s\n%s\n' "${gpu_ids[0]}" "${gpu_ids[1]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
  smoke_model 'MoE Large' "$moe_large_model_id" 'qualification-smoke-moe-large' \
    ",\"gpu_mode\":\"manual\",\"gpu_devices\":${smoke_gpu_json}"
else
  smoke_model 'MoE Large' "$moe_large_model_id" 'qualification-smoke-moe-large'
fi

log_step "dense lifecycle soak (${cycles} cycles, 8B)"
dense_model_id="$dense_lifecycle_model_id"
dense_instance_id="$(create_instance "$dense_model_id" 'Qualification Dense' 'qualification-dense' ',"gpu_mode":"auto"')"
auth_request POST "/api/v1/instances/${dense_instance_id}/start" >/dev/null
wait_state "$dense_instance_id" READY
assert_single_worker "$dense_instance_id"

rss_baseline="$(docker stats --no-stream --format '{{.MemUsage}}' "$container_name")"
for cycle in $(seq 1 "$cycles"); do
  log_step "dense lifecycle cycle ${cycle}/${cycles}"
  infer "$dense_instance_id"
  stream_infer "$dense_instance_id" "$artifact_dir/dense-stream-${cycle}.txt"
  assert_single_worker "$dense_instance_id"
  if (( cycle % 2 == 0 )); then
    auth_request POST "/api/v1/instances/${dense_instance_id}/restart" >/dev/null
    wait_state "$dense_instance_id" READY
    assert_single_worker "$dense_instance_id"
  fi
  if (( cycle % 3 == 0 )); then
    infer "$dense_instance_id" & p1=$!
    infer "$dense_instance_id" & p2=$!
    wait "$p1"; wait "$p2"
  fi
  docker stats --no-stream --format '{{json .}}' "$container_name" >>"$artifact_dir/docker-stats.jsonl"
done

# Crash the manager while a streamed inference is active. The container shell
# remains PID 1 so the owned llama-server survives long enough for startup
# reconciliation to prove ownership and terminate it.
log_step "ready-crash recovery"
curl -sS -N -X POST \
  -H "Authorization: Bearer $management_token" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$dense_instance_id\",\"messages\":[{\"role\":\"user\",\"content\":\"Write a long numbered list.\"}],\"max_tokens\":512,\"stream\":true}" \
  "$base_url/api/v1/playground/chat/completions" >"$artifact_dir/crash-stream.txt" 2>&1 & stream_pid=$!
sleep 0.5
old_worker_pid="$(auth_request GET "/api/v1/instances/${dense_instance_id}/runtime" | json_value 'data["pid"]')"
kill_manager
wait "$stream_pid" || true
[[ "$(worker_count "$dense_instance_id")" == "1" ]] || { echo "owned worker did not survive manager process crash" >&2; exit 1; }
start_manager
wait_no_worker "$dense_instance_id"

# The bearer signing key and database survive manager restart.
setting_after_restart="$(auth_request GET /api/v1/settings/general | json_value 'data["idle_unload_seconds"]["value"]')"
[[ "$setting_after_restart" == "23" ]]
auth_request POST "/api/v1/instances/${dense_instance_id}/start" >/dev/null
wait_state "$dense_instance_id" READY
new_worker_pid="$(auth_request GET "/api/v1/instances/${dense_instance_id}/runtime" | json_value 'data["pid"]')"
[[ "$new_worker_pid" != "$old_worker_pid" ]]
assert_single_worker "$dense_instance_id"

# Kill the manager during another start request to cover STARTING/LOADING
# recovery without depending on a particular model-load duration.
log_step "start-crash recovery"
auth_request POST "/api/v1/instances/${dense_instance_id}/stop" >/dev/null
wait_state "$dense_instance_id" UNLOADED
curl -sS -X POST -H "Authorization: Bearer $management_token" \
  "$base_url/api/v1/instances/${dense_instance_id}/start" >"$artifact_dir/start-during-crash.txt" 2>&1 & start_pid=$!
sleep 0.2
kill_manager
wait "$start_pid" || true
start_manager
wait_no_worker "$dense_instance_id"
auth_request POST "/api/v1/instances/${dense_instance_id}/start" >/dev/null
wait_state "$dense_instance_id" READY
assert_single_worker "$dense_instance_id"
infer "$dense_instance_id"
auth_request POST "/api/v1/instances/${dense_instance_id}/stop" >/dev/null
wait_state "$dense_instance_id" UNLOADED

# Autoload must start an unloaded Instance on first inference without creating
# duplicate workers.
log_step "autoload"
autoload_instance_id="$(create_instance "$dense_model_id" 'Qualification Autoload' 'qualification-autoload' ',"autoload_enabled":true,"gpu_mode":"auto"')"
infer "$autoload_instance_id"
wait_state "$autoload_instance_id" READY
assert_single_worker "$autoload_instance_id"
auth_request POST "/api/v1/instances/${autoload_instance_id}/stop" >/dev/null
wait_state "$autoload_instance_id" UNLOADED

# Concurrent explicit starts must single-flight to one worker. Then cancel a
# real streaming request and prove the same worker remains healthy for inference.
log_step "concurrent start + stream cancel"
concurrent_instance_id="$(create_instance "$dense_model_id" 'Qualification Concurrent' 'qualification-concurrent' ',"gpu_mode":"auto"')"
auth_request POST "/api/v1/instances/${concurrent_instance_id}/start" >"$artifact_dir/concurrent-start-1.json" & start_one=$!
auth_request POST "/api/v1/instances/${concurrent_instance_id}/start" >"$artifact_dir/concurrent-start-2.json" & start_two=$!
wait "$start_one"; wait "$start_two"
wait_state "$concurrent_instance_id" READY
assert_single_worker "$concurrent_instance_id"
cancel_stream "$concurrent_instance_id" "$artifact_dir/cancelled-stream.txt"
assert_single_worker "$concurrent_instance_id"
infer "$concurrent_instance_id"
auth_request POST "/api/v1/instances/${concurrent_instance_id}/stop" >/dev/null
wait_state "$concurrent_instance_id" UNLOADED

# Per-Instance idle unload must stop an inactive worker after the configured
# timeout. Autoload is enabled so a follow-up inference also proves recovery.
log_step "idle unload and reload"
idle_instance_id="$(create_instance "$dense_model_id" 'Qualification Idle' 'qualification-idle' ',"autoload_enabled":true,"idle_unload_seconds":1,"gpu_mode":"auto"')"
infer "$idle_instance_id"
wait_state "$idle_instance_id" READY
wait_state "$idle_instance_id" UNLOADED
infer "$idle_instance_id"
wait_state "$idle_instance_id" READY
auth_request POST "/api/v1/instances/${idle_instance_id}/stop" >/dev/null
wait_state "$idle_instance_id" UNLOADED

# Always-On reconciliation must start the Instance without an explicit launch,
# recover after an unexpected worker SIGKILL, and remain suppressible by an
# explicit Stop. Use a direct process kill here: the management Kill action is
# an operator command, while this scenario specifically qualifies crash recovery.
log_step "Always-On crash recovery"
always_instance_id="$(create_instance "$dense_model_id" 'Qualification Always On' 'qualification-always-on' ',"always_on":true,"gpu_mode":"auto"')"
wait_state "$always_instance_id" READY
assert_single_worker "$always_instance_id"
always_worker_pid="$(auth_request GET "/api/v1/instances/${always_instance_id}/runtime" | json_value 'data["pid"]')"
docker exec -u 0 "$container_name" kill -9 "$always_worker_pid"
wait_process_gone "$always_worker_pid"
wait_state_through_failure "$always_instance_id" READY
recovered_always_worker_pid="$(auth_request GET "/api/v1/instances/${always_instance_id}/runtime" | json_value 'data["pid"]')"
[[ "$recovered_always_worker_pid" != "$always_worker_pid" ]] || { echo "Always-On worker PID did not change after crash recovery" >&2; exit 1; }
assert_single_worker "$always_instance_id"
auth_request POST "/api/v1/instances/${always_instance_id}/stop" >/dev/null
wait_state "$always_instance_id" UNLOADED
sleep 3
[[ "$(runtime_state "$always_instance_id")" == "UNLOADED" ]] || { echo "manual Stop did not suppress Always-On reconcile" >&2; exit 1; }

# Resource-pressure eviction uses the 8B dense GGUF and is always pinned to
# one target GPU so the scenario behaves the same on 1-GPU and multi-GPU hosts.
# Raise per-worker VRAM demand via ctx-size until a small number of copies must
# evict; keep one copy inside a VRAM margin so the first worker can still load.
log_step "8B single-GPU pressure (choose ctx-size)"
target_gpu="${gpu_ids[0]}"
target_gpu_total="$(printf '%s' "$hardware" | python3 -c 'import json,sys; gpus=json.load(sys.stdin)["gpus"]; print(int(gpus[0]["total_bytes"]))')"
reserve_bytes=$((512 * 1024 * 1024))
usable_bytes=$((target_gpu_total > reserve_bytes ? target_gpu_total - reserve_bytes : 0))
pressure_ctx="$(python3 - "$base_url" "$management_token" "$dense_lifecycle_model_id" "$usable_bytes" <<'PY'
import json
import sys
import urllib.request

base, token, model_id, usable = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
margin = int(usable * 0.85)
for ctx in (4096, 8192, 16384, 32768, 65536):
    req = urllib.request.Request(
        f"{base}/api/v1/models/{model_id}/recommendation?context_length={ctx}",
        headers={"Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(req, timeout=120) as resp:
        rec = json.load(resp)
    memory = rec.get("memory") or {}
    offload = rec.get("offload") or {}
    vram = int(memory.get("full_offload_vram_bytes") or 0)
    devices = offload.get("devices") or []
    mode = offload.get("mode") or ""
    copies = (usable // vram) if vram > 0 else 0
    print(
        f"pressure probe ctx={ctx} vram={vram} mode={mode} devices={','.join(devices) or '-'} copies_that_fit={copies}",
        file=sys.stderr,
    )
    if vram <= 0:
        continue
    if mode == "multi_gpu" or len(devices) > 1:
        continue
    if vram > margin:
        continue
    # Active-request protection and 2-worker eviction need two copies to miss.
    if vram * 2 > usable:
        print(ctx)
        raise SystemExit(0)
print(0)
PY
)"
if [[ "$pressure_ctx" == "0" ]]; then
  echo "8B dense pressure model (${pressure_bytes} bytes) cannot create single-GPU scheduler pressure on ${target_gpu} (${target_gpu_total} bytes usable≈${usable_bytes})" >&2
  exit 1
fi
printf 'pressure ctx-size=%s target_gpu=%s usable_bytes=%s model_bytes=%s\n' \
  "$pressure_ctx" "$target_gpu" "$usable_bytes" "$pressure_bytes"
pressure_opts="\"options\":{\"ctx-size\":\"${pressure_ctx}\"}"
pressure_common=",\"gpu_mode\":\"manual\",\"gpu_devices\":[\"${target_gpu}\"],\"eviction_enabled\":true,${pressure_opts}"

# Active-request protection: the only resident worker with in-flight work must
# not be evicted to admit another copy on the same GPU.
log_step "8B pressure: active-request protection"
protected_id="$(create_instance "$dense_lifecycle_model_id" 'Qualification Pressure Protected' 'qualification-pressure-protected' "$pressure_common")"
auth_request POST "/api/v1/instances/${protected_id}/start" >/dev/null
wait_state "$protected_id" READY
assert_single_worker "$protected_id"
curl -sS -N --limit-rate 1k -X POST \
  -H "Authorization: Bearer $management_token" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$protected_id\",\"messages\":[{\"role\":\"user\",\"content\":\"Write a very long numbered list with detailed explanations.\"}],\"max_tokens\":4096,\"stream\":true}" \
  "$base_url/api/v1/playground/chat/completions" >"$artifact_dir/pressure-protected-stream.txt" 2>&1 &
protected_stream_pid=$!
# Give the gateway a moment to register the active request before challenging.
sleep 1
kill -0 "$protected_stream_pid" 2>/dev/null || {
  echo "protected pressure stream ended before the eviction challenge could run" >&2
  exit 1
}
blocked_id="$(create_instance "$dense_lifecycle_model_id" 'Qualification Pressure Blocked' 'qualification-pressure-blocked' "$pressure_common")"
if auth_request POST "/api/v1/instances/${blocked_id}/start" >"$artifact_dir/pressure-blocked-start.json" 2>"$artifact_dir/pressure-blocked-start.err"; then
  # Start may enqueue while eviction is attempted; the protected resident must
  # remain READY and the challenger must not become the sole survivor.
  sleep 2
  [[ "$(runtime_state "$protected_id")" == "READY" ]] || {
    echo "active-request protection failed: protected pressure worker is no longer READY" >&2
    kill -TERM "$protected_stream_pid" >/dev/null 2>&1 || true
    wait "$protected_stream_pid" >/dev/null 2>&1 || true
    exit 1
  }
  blocked_state="$(runtime_state "$blocked_id" || true)"
  if [[ "$blocked_state" == "READY" ]]; then
    echo "active-request protection failed: challenger became READY while protected request was active" >&2
    kill -TERM "$protected_stream_pid" >/dev/null 2>&1 || true
    wait "$protected_stream_pid" >/dev/null 2>&1 || true
    exit 1
  fi
fi
kill -TERM "$protected_stream_pid" >/dev/null 2>&1 || true
wait "$protected_stream_pid" >/dev/null 2>&1 || true
auth_request POST "/api/v1/instances/${blocked_id}/stop" >/dev/null 2>&1 || true
auth_request POST "/api/v1/instances/${protected_id}/stop" >/dev/null
wait_state "$protected_id" UNLOADED

# Idle eviction: fill the target GPU with a few high-demand workers until the
# scheduler must evict an idle peer. Cap attempts so a too-small model fails
# with a provisioning error instead of thrashing dozens of GGUF loads.
log_step "8B pressure: idle eviction"
pressure_ids=()
eviction_observed=0
max_pressure_workers=4
for index in $(seq 1 "$max_pressure_workers"); do
  log_step "8B pressure worker ${index}/${max_pressure_workers}"
  pressure_id="$(create_instance "$dense_lifecycle_model_id" "Qualification Pressure ${index}" "qualification-pressure-${index}" "$pressure_common")"
  pressure_ids+=("$pressure_id")
  if ! auth_request POST "/api/v1/instances/${pressure_id}/start" >"$artifact_dir/pressure-start-${index}.json" 2>"$artifact_dir/pressure-start-${index}.err"; then
    echo "pressure worker ${index} failed to start on ${target_gpu} (ctx-size=${pressure_ctx}); see pressure-start-${index}.err" >&2
    exit 1
  fi
  if ! wait_state "$pressure_id" READY; then
    echo "pressure worker ${index} failed readiness on ${target_gpu} (ctx-size=${pressure_ctx}); 8B single-GPU pressure could not load" >&2
    exit 1
  fi
  assert_single_worker "$pressure_id"
  if (( ${#pressure_ids[@]} > 1 )); then
    for prior in "${pressure_ids[@]:0:${#pressure_ids[@]}-1}"; do
      if [[ "$(runtime_state "$prior")" == "UNLOADED" ]]; then
        eviction_observed=1
        break 2
      fi
    done
  fi
done
[[ "$eviction_observed" == "1" ]] || {
  echo "GPU pressure was not reached after ${max_pressure_workers} 8B workers on ${target_gpu} (model=${pressure_bytes} bytes, ctx-size=${pressure_ctx}, gpu=${target_gpu_total} bytes)" >&2
  exit 1
}
for pressure_id in "${pressure_ids[@]}"; do
  auth_request POST "/api/v1/instances/${pressure_id}/stop" >/dev/null 2>&1 || true
done

# Dense 12B is the multi-GPU dense placement model. Pin both cards so the
# comma-separated --device and generated --tensor-split launch path is proven.
# A single-GPU host cannot load this GGUF without OOM, so skip the start there.
log_step "12B multi-GPU dense placement"
if (( ${#gpu_ids[@]} >= 2 )); then
  dense_multi_gpu_json="$(printf '%s\n%s\n' "${gpu_ids[0]}" "${gpu_ids[1]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
  expected_dense_multi_devices="${gpu_ids[0]},${gpu_ids[1]}"
  dense_multi_instance_id="$(create_instance "$dense_multi_model_id" 'Qualification Dense Multi' 'qualification-dense-multi' ",\"gpu_mode\":\"manual\",\"gpu_devices\":${dense_multi_gpu_json},\"tensor_split\":\"1,1\"")"
  auth_request POST "/api/v1/instances/${dense_multi_instance_id}/start" >/dev/null
  wait_state "$dense_multi_instance_id" READY
  assert_single_worker "$dense_multi_instance_id"
  infer "$dense_multi_instance_id"
  verify_dense_multi_launch "$dense_multi_instance_id" "$expected_dense_multi_devices" \
    "$artifact_dir/dense-multi-worker-args.txt" "$artifact_dir/dense-multi-worker-environ.txt"
  auth_request POST "/api/v1/instances/${dense_multi_instance_id}/stop" >/dev/null
  wait_state "$dense_multi_instance_id" UNLOADED
  dense_multi_exercised=1
else
  printf 'skipping 12B dense multi-GPU start on a single-GPU host\n' | tee "$artifact_dir/dense-multi-skipped.txt"
fi

# Small MoE: always exercise n-cpu-moe on the first GPU.
log_step "MoE small n-cpu-moe"
moe_small_gpu_json="$(printf '%s\n' "${gpu_ids[0]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
moe_small_instance_id="$(auth_request POST /api/v1/instances \
  "{\"model_id\":\"$moe_small_model_id\",\"name\":\"Qualification MoE Small\",\"slug\":\"qualification-moe-small\",\"gpu_mode\":\"manual\",\"gpu_devices\":$moe_small_gpu_json,\"options\":{\"n-cpu-moe\":\"1\"}}" \
  | json_value 'data["id"]')"
auth_request POST "/api/v1/instances/${moe_small_instance_id}/start" >/dev/null
wait_state "$moe_small_instance_id" READY
assert_single_worker "$moe_small_instance_id"
infer "$moe_small_instance_id"
verify_moe_launch "$moe_small_instance_id" "${gpu_ids[0]}" \
  "$artifact_dir/moe-worker-args.txt" "$artifact_dir/moe-worker-environ.txt"
auth_request POST "/api/v1/instances/${moe_small_instance_id}/stop" >/dev/null
wait_state "$moe_small_instance_id" UNLOADED

# Large MoE: on multi-GPU hosts pin both devices so the comma-separated --device
# launch path is covered. On a single-GPU host still prove n-cpu-moe works.
log_step "MoE large n-cpu-moe / placement"
if (( ${#gpu_ids[@]} >= 2 )); then
  moe_large_gpu_json="$(printf '%s\n%s\n' "${gpu_ids[0]}" "${gpu_ids[1]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
  expected_moe_large_devices="${gpu_ids[0]},${gpu_ids[1]}"
else
  moe_large_gpu_json="$(printf '%s\n' "${gpu_ids[0]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
  expected_moe_large_devices="${gpu_ids[0]}"
fi
moe_large_instance_id="$(auth_request POST /api/v1/instances \
  "{\"model_id\":\"$moe_large_model_id\",\"name\":\"Qualification MoE Large\",\"slug\":\"qualification-moe-large\",\"gpu_mode\":\"manual\",\"gpu_devices\":$moe_large_gpu_json,\"options\":{\"n-cpu-moe\":\"1\"}}" \
  | json_value 'data["id"]')"
auth_request POST "/api/v1/instances/${moe_large_instance_id}/start" >/dev/null
wait_state "$moe_large_instance_id" READY
assert_single_worker "$moe_large_instance_id"
infer "$moe_large_instance_id"
verify_moe_launch "$moe_large_instance_id" "$expected_moe_large_devices" \
  "$artifact_dir/moe-large-worker-args.txt" "$artifact_dir/moe-large-worker-environ.txt"
auth_request POST "/api/v1/instances/${moe_large_instance_id}/stop" >/dev/null
wait_state "$moe_large_instance_id" UNLOADED
gpu_json="$moe_large_gpu_json"

rss_final="$(docker stats --no-stream --format '{{.MemUsage}}' "$container_name")"
docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python3 - "$artifact_dir/manifest.json" <<PY
import json, os, sys
checks = [
    "model-matrix-smoke",
    "fresh-install-and-settings-persistence",
    "real-inference-and-streaming",
    "client-cancellation",
    "concurrent-inference",
    "concurrent-start-single-flight",
    "autoload",
    "idle-unload-and-reload",
    "always-on-reconciliation",
    "resource-pressure-active-request-protection",
    "resource-pressure-placement-and-eviction",
    "stop-restart-cycles",
    "ready-crash-recovery",
    "start-crash-recovery",
    "single-worker-invariant",
    "moe-cpu-expert-offload",
    "moe-large-cpu-expert-offload",
]
if ${#gpu_ids[@]} >= 2:
    checks.append("multi-gpu-dense-placement")
    checks.append("multi-gpu-placement")
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({
        "scenario": "gpu-release-qualification",
        "commit": os.environ.get("GITHUB_SHA", ""),
        "image": "${image}",
        "started_at": "${started_at}",
        "finished_at": "${finished_at}",
        "cycles": int("${cycles}"),
        "models_dir": "${models_dir}",
        "dense_lifecycle_model": "$(basename "$dense_lifecycle_host")",
        "dense_multi_model": "$(basename "$dense_multi_host")",
        "dense_multi_exercised": ${dense_multi_exercised},
        "dense_pressure_model": "$(basename "$dense_lifecycle_host")",
        "dense_pressure_bytes": int("${pressure_bytes}"),
        "pressure_ctx_size": int("${pressure_ctx}"),
        "pressure_target_gpu": "${target_gpu}",
        "moe_small_model": "$(basename "$moe_small_host")",
        "moe_large_model": "$(basename "$moe_large_host")",
        "gpu_ids": ${gpu_json},
        "rss_baseline": "${rss_baseline}",
        "rss_final": "${rss_final}",
        "checks": checks,
        "result": "pass"
    }, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

echo "GPU release qualification passed"
