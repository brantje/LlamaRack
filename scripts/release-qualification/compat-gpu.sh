#!/usr/bin/env bash
set -euo pipefail

# Start an immutable CUDA release candidate with one real qualification GGUF,
# provision a disposable OpenAI-compatible fixture, and run tests/compat through
# the same public /v1 surface a user sees.
image="${1:?usage: compat-gpu.sh <cuda-image> <models-dir>}"
models_dir_arg="${2:?models directory is required}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
artifact_dir="${QUALIFICATION_ARTIFACT_DIR:-$repo_root/artifacts/release-qualification/compatibility}"
mkdir -p "$artifact_dir"

for command in docker curl python3 node npm; do
  command -v "$command" >/dev/null || { echo "missing compatibility prerequisite: $command" >&2; exit 1; }
done
[[ -d "$models_dir_arg" ]] || { echo "models directory not found: $models_dir_arg" >&2; exit 1; }
models_dir="$(cd "$models_dir_arg" && pwd)"

dense_host="${GPU_QUALIFICATION_DENSE_LIFECYCLE:-}"
if [[ -z "$dense_host" ]]; then
  for candidate in qualification-llama-8B.gguf qualification.gguf; do
    if [[ -f "$models_dir/$candidate" ]]; then
      dense_host="$models_dir/$candidate"
      break
    fi
  done
fi
[[ -n "$dense_host" && -f "$dense_host" ]] || {
  echo "compatibility qualification requires qualification-llama-8B.gguf or qualification.gguf" >&2
  exit 1
}
dense_host="$(cd "$(dirname "$dense_host")" && pwd)/$(basename "$dense_host")"

config_dir="$(mktemp -d)"
chmod 0777 "$config_dir"
container_name="llamarack-compat-qualification-$$"
docker_probe_host="${GPU_DOCKER_HOST:-127.0.0.1}"
docker_publish_host="127.0.0.1"

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

with open("/proc/net/route", encoding="utf-8") as routes:
    next(routes, None)
    for line in routes:
        fields = line.split()
        if len(fields) >= 4 and fields[1] == "00000000" and int(fields[3], 16) & 0x2:
            print(socket.inet_ntoa(struct.pack("<L", int(fields[2], 16))))
            raise SystemExit(0)
raise SystemExit("unable to resolve Docker host; set GPU_DOCKER_HOST")
PY
)"
fi
if [[ "$docker_probe_host" != "127.0.0.1" ]]; then
  docker_publish_host="$(python3 - "$docker_probe_host" <<'PY'
import socket, sys
print(socket.gethostbyname(sys.argv[1]))
PY
)"
fi

cleanup() {
  docker exec "$container_name" cat /config/manager.log >"$artifact_dir/manager.log" 2>/dev/null || true
  docker exec -u 0 "$container_name" sh -c 'chmod -R a+rwX /config' >/dev/null 2>&1 || true
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$config_dir"
}
trap cleanup EXIT

docker pull "$image"
docker image inspect "$image" >"$artifact_dir/docker-image.json"
docker run -d --gpus all --name "$container_name" \
  --entrypoint sh \
  -p "${docker_publish_host}::8000" \
  -v "$config_dir:/config" \
  -v "$dense_host:/models/compat.gguf:ro" \
  "$image" -c 'while :; do sleep 3600; done' >/dev/null
port="$(docker port "$container_name" 8000/tcp | awk -F: 'NR == 1 { print $NF }')"
[[ "$port" =~ ^[0-9]+$ ]] || { echo "unable to resolve published manager port" >&2; exit 1; }
manager_url="http://${docker_probe_host}:${port}"

docker exec -d "$container_name" sh -c 'exec /usr/local/bin/llamarack >>/config/manager.log 2>&1'
for _ in $(seq 1 120); do
  curl -fsS "$manager_url/health" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "$manager_url/health" >/dev/null || { echo "candidate manager did not become healthy" >&2; exit 1; }

json_value() {
  local expression="$1"
  python3 -c "import json,sys; data=json.load(sys.stdin); print(${expression})"
}

curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"compat-admin","password":"compat-qualification-password"}' \
  "$manager_url/api/v1/auth/bootstrap" >/dev/null
login="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"compat-admin","password":"compat-qualification-password"}' \
  "$manager_url/api/v1/auth/login")"
management_token="$(printf '%s' "$login" | json_value 'data["access_token"]')"
[[ -n "$management_token" ]]

auth_request() {
  local method="$1" path="$2" body="${3:-}" tmp status
  tmp="$(mktemp)"
  if [[ -n "$body" ]]; then
    status="$(curl -sS --connect-timeout 10 --max-time 180 -o "$tmp" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $management_token" -H 'Content-Type: application/json' \
      -d "$body" "$manager_url$path")" || true
  else
    status="$(curl -sS --connect-timeout 10 --max-time 180 -o "$tmp" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $management_token" "$manager_url$path")" || true
  fi
  if [[ ! "$status" =~ ^2 ]]; then
    echo "request failed: $method $path HTTP $status" >&2
    cat "$tmp" >&2 || true
    rm -f "$tmp"
    return 1
  fi
  cat "$tmp"
  rm -f "$tmp"
}

service_account="$(auth_request POST /api/v1/admin/service-accounts '{"name":"Compatibility Qualification"}')"
service_account_id="$(printf '%s' "$service_account" | json_value 'data["id"]')"
key_response="$(auth_request POST /api/v1/api-keys \
  "{\"name\":\"Compatibility Qualification\",\"key_type\":\"inference\",\"owner_service_account_id\":\"$service_account_id\",\"instance_ids\":[]}")"
inference_key="$(printf '%s' "$key_response" | json_value 'data["secret"]')"
[[ "$inference_key" == sk-* ]]

model_response="$(auth_request POST /api/v1/models \
  '{"name":"Compatibility Dense","gguf_path":"/models/compat.gguf","context_length":4096}')"
model_id="$(printf '%s' "$model_response" | json_value 'data["model"]["id"]')"
instance_response="$(auth_request POST /api/v1/instances \
  "{\"model_id\":\"$model_id\",\"name\":\"Compatibility Dense\",\"slug\":\"compatibility-dense\",\"autoload_enabled\":true,\"gpu_mode\":\"auto\",\"request_log_mode\":\"full\"}")"
instance_id="$(printf '%s' "$instance_response" | json_value 'data["id"]')"
auth_request POST "/api/v1/instances/${instance_id}/start" >/dev/null

for _ in $(seq 1 180); do
  runtime="$(auth_request GET "/api/v1/instances/${instance_id}/runtime")"
  state="$(printf '%s' "$runtime" | json_value 'data["state"]')"
  [[ "$state" == "READY" ]] && break
  [[ "$state" == "FAILED" ]] && { echo "compatibility fixture failed to start" >&2; printf '%s\n' "$runtime" >&2; exit 1; }
  sleep 1
done
[[ "$(auth_request GET "/api/v1/instances/${instance_id}/runtime" | json_value 'data["state"]')" == "READY" ]] || {
  echo "compatibility fixture did not reach READY" >&2
  exit 1
}

export LLAMARACK_BASE_URL="$manager_url/v1"
export LLAMARACK_API_KEY="$inference_key"
export LLAMARACK_CHAT_MODEL="$instance_id"
export LLAMARACK_RESPONSES_MODEL="$instance_id"
export LLAMARACK_MANAGEMENT_BASE_URL="$manager_url"
export LLAMARACK_MANAGEMENT_KEY="$management_token"
export LLAMARACK_LIFECYCLE_MODEL="$instance_id"
export LLAMARACK_REQUIRED_CAPABILITIES="lifecycle_ready,lifecycle_autoload,lifecycle_no_autoload"
export LLAMARACK_REQUIRE_LITELLM_PROXY=1
export LLAMARACK_TARGET_ID="$image"
export LLAMARACK_ARTIFACT_DIR="$artifact_dir/evidence"

bash "$script_dir/compat.sh"

# Record only non-secret fixture identity/configuration.
printf 'target=%s\nmodel=%s\ninstance=%s\ngguf=%s\n' \
  "$image" "$model_id" "$instance_id" "$(basename "$dense_host")" \
  >"$artifact_dir/fixture.txt"
