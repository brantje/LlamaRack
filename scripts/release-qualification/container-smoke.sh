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
port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
base_url="http://127.0.0.1:${port}"
log_file="$artifact_dir/${variant}-manager.log"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  [[ -n "${QUALIFICATION_KEEP_WORKDIR:-}" ]] || rm -rf "$work_dir"
}
trap cleanup EXIT

start_manager() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker run -d --name "$container_name" \
    -p "127.0.0.1:${port}:8000" \
    -v "$config_dir:/config" \
    -v "$models_dir:/models" \
    "$image" >/dev/null
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

auth_request PUT /api/v1/settings/general '{"idle_unload_seconds":17}' >/dev/null
setting_value="$(auth_request GET /api/v1/settings/general | json_value 'data["idle_unload_seconds"]["value"]')"
[[ "$setting_value" == "17" ]] || { echo "setting write did not persist in-process" >&2; exit 1; }

service_account_response="$(auth_request POST /api/v1/admin/service-accounts '{"name":"Release Qualification"}')"
service_account_id="$(printf '%s' "$service_account_response" | json_value 'data["id"]')"
[[ -n "$service_account_id" ]]

# Exercise key creation without retaining or logging the generated secret.
auth_request POST /api/v1/api-keys \
  "{\"name\":\"Release Qualification\",\"key_type\":\"full\",\"owner_service_account_id\":\"$service_account_id\"}" \
  | SERVICE_ACCOUNT_ID="$service_account_id" python3 -c '
import json, os, sys
payload = json.load(sys.stdin)
assert payload["key"]["owner_id"] == os.environ["SERVICE_ACCOUNT_ID"]
assert payload["secret"].startswith("sk-")
'

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

docker logs "$container_name" >"$log_file" 2>&1 || true
finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
python3 - "$artifact_dir/${variant}-manifest.json" <<PY
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({
        "scenario": "container-smoke",
        "variant": "${variant}",
        "image": "${image}",
        "image_id": "${image_id}",
        "commit": os.environ.get("GITHUB_SHA", ""),
        "started_at": "${started_at}",
        "finished_at": "${finished_at}",
        "checks": [
            "health",
            "static_frontend",
            "fresh_bootstrap_and_login",
            "sqlite_migrations",
            "settings_persistence",
            "service_account_and_api_key_creation",
            "durable_bearer_session",
            "restart"
        ],
        "result": "pass"
    }, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

echo "${variant} container qualification passed for ${image}"
