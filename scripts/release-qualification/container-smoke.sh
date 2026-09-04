#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: container-smoke.sh <image> [cpu|cuda]}"
variant="${2:-cpu}"
case "$variant" in cpu|cuda) ;; *) echo "variant must be cpu or cuda" >&2; exit 2 ;; esac

for command in docker curl python3; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done

work_dir="$(mktemp -d)"
config_dir="$work_dir/config"
models_dir="$work_dir/models"
artifact_dir="${QUALIFICATION_ARTIFACT_DIR:-$work_dir/artifacts}"
mkdir -p "$config_dir" "$models_dir" "$artifact_dir"
chmod 0777 "$config_dir" "$models_dir"
container_name="llamarack-qualification-${variant}-$$"
base_url=""
log_file="$artifact_dir/${variant}-manager.log"
identity_file="$artifact_dir/${variant}-identity.json"

cleanup() {
  docker logs "$container_name" >"$log_file" 2>&1 || true
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  [[ -n "${QUALIFICATION_KEEP_WORKDIR:-}" ]] || rm -rf "$work_dir"
}
trap cleanup EXIT

start_manager() {
  local port
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker run -d --name "$container_name" \
    -p '127.0.0.1::8000' \
    -v "$config_dir:/config" \
    -v "$models_dir:/models" \
    "$image" >/dev/null
  port="$(docker port "$container_name" 8000/tcp | awk -F: 'NR == 1 { print $NF }')"
  [[ "$port" =~ ^[0-9]+$ ]] || { echo "unable to resolve Docker-published manager port" >&2; return 1; }
  base_url="http://127.0.0.1:${port}"
  for _ in $(seq 1 90); do
    curl -fsS "$base_url/health" >/dev/null 2>&1 && return 0
    if ! docker inspect -f '{{.State.Running}}' "$container_name" 2>/dev/null | grep -qx true; then
      docker logs "$container_name" >"$log_file" 2>&1 || true
      cat "$log_file" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "$container_name" >"$log_file" 2>&1 || true
  cat "$log_file" >&2
  return 1
}

json_value() {
  local expression="$1"
  python3 -c "import json,sys; data=json.load(sys.stdin); print(${expression})"
}

auth_request() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" \
      -H "Authorization: Bearer $management_token" \
      -H 'Content-Type: application/json' \
      -d "$body" "$base_url$path"
  else
    curl -fsS -X "$method" \
      -H "Authorization: Bearer $management_token" \
      "$base_url$path"
  fi
}

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
image_id="$(docker image inspect -f '{{.Id}}' "$image")"
start_manager

curl -fsS "$base_url/health" | python3 -c 'import json,sys; assert json.load(sys.stdin)["status"] == "ok"'
[[ -n "$(curl -fsS "$base_url/")" ]] || { echo "empty frontend response" >&2; exit 1; }
curl -fsS "$base_url/api/v1/auth/bootstrap" | python3 -c 'import json,sys; assert json.load(sys.stdin)["required"] is True'

curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"qualification-admin","password":"qualification-password-120"}' \
  "$base_url/api/v1/auth/bootstrap" >/dev/null
login_response="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"qualification-admin","password":"qualification-password-120"}' \
  "$base_url/api/v1/auth/login")"
management_token="$(printf '%s' "$login_response" | json_value 'data["access_token"]')"
[[ -n "$management_token" ]]

identity_response="$(auth_request GET /api/v1/system)"
printf '%s\n' "$identity_response" > "$identity_file"
python3 - "$identity_file" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
identity = payload.get("identity") or {}
llama = identity.get("llama_cpp") or {}

for field in ("version", "channel", "variant"):
    if not identity.get(field):
        raise SystemExit(f"system identity is missing {field}")

checks = {
    "EXPECTED_LLAMARACK_VERSION": identity.get("version", ""),
    "EXPECTED_LLAMARACK_COMMIT": identity.get("commit", ""),
    "EXPECTED_LLAMARACK_CHANNEL": identity.get("channel", ""),
    "EXPECTED_RUNTIME_VARIANT": identity.get("variant", ""),
    "EXPECTED_LLAMA_CPP_RELEASE": llama.get("release", ""),
    "EXPECTED_LLAMA_CPP_BUILD": llama.get("build", ""),
    "EXPECTED_LLAMA_CPP_IMAGE": llama.get("image", ""),
}
for env_name, actual in checks.items():
    expected = os.environ.get(env_name, "")
    if expected and actual != expected:
        raise SystemExit(f"{env_name} mismatch: expected {expected!r}, got {actual!r}")

if os.environ.get("EXPECTED_RELEASE_BUILD") == "true":
    if identity.get("version") == "development":
        raise SystemExit("official release build reports development version")
    if identity.get("channel") != "release":
        raise SystemExit("official release build does not report release channel")
    if not identity.get("commit"):
        raise SystemExit("official release build is missing commit")
    if not llama.get("release") or not llama.get("build"):
        raise SystemExit("official release build is missing llama.cpp release/build identity")
PY

auth_request PUT /api/v1/settings/general '{"idle_unload_seconds":17}' >/dev/null
setting_value="$(auth_request GET /api/v1/settings/general | json_value 'data["idle_unload_seconds"]["value"]')"
[[ "$setting_value" == "17" ]] || { echo "setting write did not persist in-process" >&2; exit 1; }

service_account_response="$(auth_request POST /api/v1/admin/service-accounts '{"name":"Release Qualification"}')"
service_account_id="$(printf '%s' "$service_account_response" | json_value 'data["id"]')"
[[ -n "$service_account_id" ]]

# Verify key creation while retaining only its non-secret identifier for the
# post-restart durability assertion.
api_key_response="$(auth_request POST /api/v1/api-keys \
  "{\"name\":\"Release Qualification\",\"key_type\":\"full\",\"owner_service_account_id\":\"$service_account_id\"}")"
printf '%s' "$api_key_response" \
  | SERVICE_ACCOUNT_ID="$service_account_id" python3 -c '
import json, os, sys
payload = json.load(sys.stdin)
assert payload["key"]["owner_id"] == os.environ["SERVICE_ACCOUNT_ID"]
assert payload["secret"].startswith("sk-")
'
api_key_id="$(printf '%s' "$api_key_response" | json_value 'data["key"]["id"]')"
unset api_key_response
[[ -n "$api_key_id" ]]

# A graceful restart with the same data directory exercises SQLite migrations,
# durable signing-key initialization, settings persistence, and auth state.
docker stop -t 20 "$container_name" >/dev/null
docker logs "$container_name" >"$log_file" 2>&1 || true
docker rm "$container_name" >/dev/null
start_manager

curl -fsS "$base_url/health" >/dev/null
curl -fsS "$base_url/api/v1/auth/bootstrap" | python3 -c 'import json,sys; assert json.load(sys.stdin)["required"] is False'
setting_value="$(auth_request GET /api/v1/settings/general | json_value 'data["idle_unload_seconds"]["value"]')"
[[ "$setting_value" == "17" ]] || { echo "setting was lost after restart" >&2; exit 1; }
auth_request GET /api/v1/admin/service-accounts \
  | SERVICE_ACCOUNT_ID="$service_account_id" python3 -c '
import json, os, sys
items = json.load(sys.stdin)
assert any(item["id"] == os.environ["SERVICE_ACCOUNT_ID"] for item in items)
'
auth_request GET /api/v1/api-keys \
  | API_KEY_ID="$api_key_id" python3 -c '
import json, os, sys
items = json.load(sys.stdin)
assert any(item["id"] == os.environ["API_KEY_ID"] for item in items)
'

docker logs "$container_name" >"$log_file" 2>&1 || true
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
IDENTITY_FILE="$identity_file" python3 - "$artifact_dir/${variant}-manifest.json" <<PY
import json, os, sys
with open(os.environ["IDENTITY_FILE"], encoding="utf-8") as identity_handle:
    identity = json.load(identity_handle).get("identity", {})
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({
        "scenario": "container-smoke",
        "variant": "${variant}",
        "image": "${image}",
        "image_id": "${image_id}",
        "commit": os.environ.get("GITHUB_SHA", ""),
        "identity": identity,
        "started_at": "${started_at}",
        "finished_at": "${finished_at}",
        "checks": [
            "health",
            "static_frontend",
            "build_identity",
            "fresh_bootstrap_and_login",
            "sqlite_migrations",
            "settings_persistence",
            "service_account_persistence",
            "api_key_persistence",
            "durable_bearer_session",
            "restart"
        ],
        "result": "pass"
    }, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

echo "${variant} container qualification passed for ${image}"
