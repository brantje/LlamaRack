#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: gpu-soak.sh <cuda-image> <dense-gguf> <moe-gguf> [cycles]}"
dense_host="${2:?dense GGUF path is required}"
moe_host="${3:?MoE GGUF path is required}"
cycles="${4:-8}"

for command in docker curl python3 nvidia-smi; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -f "$dense_host" ]] || { echo "dense qualification model not found: $dense_host" >&2; exit 1; }
[[ -f "$moe_host" ]] || { echo "MoE qualification model not found: $moe_host" >&2; exit 1; }

artifact_dir="${QUALIFICATION_ARTIFACT_DIR:-$(pwd)/artifacts/release-qualification/gpu}"
mkdir -p "$artifact_dir"
config_dir="$(mktemp -d)"
chmod 0777 "$config_dir"
container_name="llamarack-gpu-qualification-$$"
base_url=""
dense_dir="$(cd "$(dirname "$dense_host")" && pwd)"
moe_dir="$(cd "$(dirname "$moe_host")" && pwd)"
dense_path="/qualification/dense/$(basename "$dense_host")"
moe_path="/qualification/moe/$(basename "$moe_host")"

cleanup() {
  docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
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
  -p '127.0.0.1::8000' \
  -v "$config_dir:/config" \
  -v "$dense_dir:/qualification/dense:ro" \
  -v "$moe_dir:/qualification/moe:ro" \
  "$image" -c 'while :; do sleep 3600; done' >/dev/null
port="$(docker port "$container_name" 8000/tcp | awk -F: 'NR == 1 { print $NF }')"
[[ "$port" =~ ^[0-9]+$ ]] || { echo "unable to resolve Docker-published manager port" >&2; exit 1; }
base_url="http://127.0.0.1:${port}"

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
  docker exec "$container_name" sh -c '
    for p in /proc/[0-9]*; do
      [ "$(cat "$p/comm" 2>/dev/null)" = "llamarack" ] && { echo "${p##*/}"; exit 0; }
    done
    exit 1
  '
}

kill_manager() {
  local pid
  pid="$(manager_pid)"
  docker exec "$container_name" kill -9 "$pid"
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

auth_request() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" -H "Authorization: Bearer $management_token" \
      -H 'Content-Type: application/json' -d "$body" "$base_url$path"
  else
    curl -fsS -X "$method" -H "Authorization: Bearer $management_token" "$base_url$path"
  fi
}

wait_state() {
  local instance_id="$1" wanted="$2"
  for _ in $(seq 1 180); do
    state="$(auth_request GET "/api/v1/instances/${instance_id}/runtime" | json_value 'data["state"]')"
    [[ "$state" == "$wanted" ]] && return 0
    [[ "$state" == "FAILED" ]] && {
      auth_request GET "/api/v1/instances/${instance_id}/runtime" >&2 || true
      return 1
    }
    sleep 1
  done
  echo "instance ${instance_id} did not reach ${wanted}" >&2
  return 1
}

worker_count() {
  local instance_id="$1"
  docker exec "$container_name" sh -c '
    instance="$1"; count=0
    for env in /proc/[0-9]*/environ; do
      if tr "\000" "\n" <"$env" 2>/dev/null | grep -qx "LLAMARACK_INSTANCE_ID=$instance"; then
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
    tr "\000" "\n" <"/proc/${pid}/environ" 2>/dev/null \
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

create_model() {
  local name="$1" path="$2"
  auth_request POST /api/v1/models "{\"name\":\"$name\",\"gguf_path\":\"$path\",\"context_length\":4096}" \
    | json_value 'data["model"]["id"]'
}

create_instance() {
  local model_id="$1" name="$2" slug="$3" body="$4"
  auth_request POST /api/v1/instances \
    "{\"model_id\":\"$model_id\",\"name\":\"$name\",\"slug\":\"$slug\"${body}}" \
    | json_value 'data["id"]'
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
[[ ${#gpu_ids[@]} -ge 2 ]] || { echo "release qualification requires at least two visible GPUs" >&2; exit 1; }

dense_model_id="$(create_model 'Qualification Dense' "$dense_path")"
dense_instance_id="$(create_instance "$dense_model_id" 'Qualification Dense' 'qualification-dense' ',"gpu_mode":"auto"')"
auth_request POST "/api/v1/instances/${dense_instance_id}/start" >/dev/null
wait_state "$dense_instance_id" READY
assert_single_worker "$dense_instance_id"

rss_baseline="$(docker stats --no-stream --format '{{.MemUsage}}' "$container_name")"
for cycle in $(seq 1 "$cycles"); do
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

# MoE qualification deliberately uses both GPUs and an explicit expert spill so
# the runtime path from #114 is exercised even when the chosen MoE would fit on
# one device without offload.
moe_model_id="$(create_model 'Qualification MoE' "$moe_path")"
gpu_json="$(printf '%s\n%s\n' "${gpu_ids[0]}" "${gpu_ids[1]}" | python3 -c 'import json,sys; print(json.dumps([x.strip() for x in sys.stdin if x.strip()]))')"
moe_instance_id="$(auth_request POST /api/v1/instances \
  "{\"model_id\":\"$moe_model_id\",\"name\":\"Qualification MoE\",\"slug\":\"qualification-moe\",\"gpu_mode\":\"manual\",\"gpu_devices\":$gpu_json,\"options\":{\"n-cpu-moe\":\"1\"}}" \
  | json_value 'data["id"]')"
auth_request POST "/api/v1/instances/${moe_instance_id}/start" >/dev/null
wait_state "$moe_instance_id" READY
assert_single_worker "$moe_instance_id"
infer "$moe_instance_id"
moe_worker_pid="$(auth_request GET "/api/v1/instances/${moe_instance_id}/runtime" | json_value 'data["pid"]')"
assert_worker_identity "$moe_worker_pid" "$moe_instance_id"
docker exec "$container_name" sh -c "tr '\\000' '\\n' </proc/${moe_worker_pid}/cmdline" \
  >"$artifact_dir/moe-worker-args.txt"
python3 - "$artifact_dir/moe-worker-args.txt" "${gpu_ids[0]},${gpu_ids[1]}" <<'PY'
import sys
args = [line.rstrip("\n") for line in open(sys.argv[1], encoding="utf-8")]
expected_devices = sys.argv[2]
assert "--n-cpu-moe" in args, args
index = args.index("--device")
assert index + 1 < len(args), args
assert args[index + 1] == expected_devices, (args, expected_devices)
PY
docker exec "$container_name" sh -c "tr '\\000' '\\n' </proc/${moe_worker_pid}/environ" \
  >"$artifact_dir/moe-worker-environ.txt"
grep -qx "LLAMARACK_INSTANCE_ID=${moe_instance_id}" "$artifact_dir/moe-worker-environ.txt"
auth_request POST "/api/v1/instances/${moe_instance_id}/stop" >/dev/null
wait_state "$moe_instance_id" UNLOADED

rss_final="$(docker stats --no-stream --format '{{.MemUsage}}' "$container_name")"
docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python3 - "$artifact_dir/manifest.json" <<PY
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({
        "scenario": "gpu-release-qualification",
        "commit": os.environ.get("GITHUB_SHA", ""),
        "image": "${image}",
        "started_at": "${started_at}",
        "finished_at": "${finished_at}",
        "cycles": int("${cycles}"),
        "dense_model": "$(basename "$dense_host")",
        "moe_model": "$(basename "$moe_host")",
        "gpu_ids": ${gpu_json},
        "rss_baseline": "${rss_baseline}",
        "rss_final": "${rss_final}",
        "checks": [
            "fresh-install-and-settings-persistence",
            "real-inference-and-streaming",
            "concurrent-inference",
            "stop-restart-cycles",
            "ready-crash-recovery",
            "start-crash-recovery",
            "single-worker-invariant",
            "multi-gpu-placement",
            "moe-cpu-expert-offload"
        ],
        "result": "pass"
    }, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

echo "GPU release qualification passed"
