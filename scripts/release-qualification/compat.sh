#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
compat_dir="$repo_root/tests/compat"
artifact_dir="${LLAMARACK_ARTIFACT_DIR:-$repo_root/artifacts/compat}"
mkdir -p "$artifact_dir"
export LLAMARACK_ARTIFACT_DIR="$artifact_dir"

for command in python3 node npm curl; do
  command -v "$command" >/dev/null || { echo "compatibility qualification requires $command" >&2; exit 1; }
done

: "${LLAMARACK_BASE_URL:?LLAMARACK_BASE_URL must include /v1}"
: "${LLAMARACK_API_KEY:?LLAMARACK_API_KEY is required}"
: "${LLAMARACK_CHAT_MODEL:?LLAMARACK_CHAT_MODEL is required}"

python3 "$compat_dir/validate_contract.py"
python3 -m py_compile "$compat_dir"/*.py
node --check "$compat_dir/node/openai-probe.mjs"

tmp_dir="$(mktemp -d)"
openai_venv="$tmp_dir/openai-venv"
litellm_venv="$tmp_dir/litellm-venv"
proxy_pid=""
cleanup() {
  if [[ -n "$proxy_pid" ]]; then
    kill "$proxy_pid" >/dev/null 2>&1 || true
    wait "$proxy_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

# Keep the current OpenAI SDK isolated from LiteLLM's OpenAI <3 dependency.
python3 -m venv "$openai_venv"
"$openai_venv/bin/python" -m pip install --disable-pip-version-check --no-input \
  -r "$compat_dir/python/requirements.txt" \
  >"$artifact_dir/python-install.log" 2>&1

python3 -m venv "$litellm_venv"
"$litellm_venv/bin/python" -m pip install --disable-pip-version-check --no-input \
  -r "$compat_dir/python/litellm-requirements.txt" \
  >"$artifact_dir/litellm-install.log" 2>&1

npm ci --prefix "$compat_dir/node" --ignore-scripts \
  >"$artifact_dir/node-install.log" 2>&1

export PYTHONPATH="$compat_dir${PYTHONPATH:+:$PYTHONPATH}"
"$openai_venv/bin/python" "$compat_dir/openai_python_probe.py"
node "$compat_dir/node/openai-probe.mjs"
"$openai_venv/bin/python" "$compat_dir/protocol_lifecycle_probe.py"

proxy_port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
proxy_master_key="$(python3 - <<'PY'
import secrets
print("sk-" + secrets.token_urlsafe(32))
PY
)"
proxy_config="$tmp_dir/litellm.yaml"
cat >"$proxy_config" <<EOF
model_list:
  - model_name: ${LLAMARACK_CHAT_MODEL}
    litellm_params:
      model: openai/${LLAMARACK_CHAT_MODEL}
      api_base: ${LLAMARACK_BASE_URL}
      api_key: os.environ/LLAMARACK_API_KEY
general_settings:
  master_key: os.environ/LLAMARACK_LITELLM_MASTER_KEY
  forward_client_headers_to_llm_api: true
EOF

export LLAMARACK_LITELLM_MASTER_KEY="$proxy_master_key"
"$litellm_venv/bin/litellm" --config "$proxy_config" --host 127.0.0.1 --port "$proxy_port" \
  >"$artifact_dir/litellm-proxy.log" 2>&1 &
proxy_pid=$!
proxy_url="http://127.0.0.1:${proxy_port}"
proxy_ready=0
for _ in $(seq 1 90); do
  if curl -fsS --connect-timeout 2 --max-time 5 \
    -H "Authorization: Bearer $proxy_master_key" \
    "$proxy_url/v1/models" >/dev/null 2>&1; then
    proxy_ready=1
    break
  fi
  kill -0 "$proxy_pid" >/dev/null 2>&1 || break
  sleep 1
done
if [[ "$proxy_ready" != "1" ]]; then
  echo "LiteLLM Proxy did not become ready" >&2
  tail -n 200 "$artifact_dir/litellm-proxy.log" >&2 || true
  exit 1
fi

export LLAMARACK_LITELLM_PROXY_URL="$proxy_url/v1"
export LLAMARACK_LITELLM_PROXY_KEY="$proxy_master_key"
"$litellm_venv/bin/python" "$compat_dir/litellm_probe.py"
"$openai_venv/bin/python" "$compat_dir/summarize.py"

# Keep logs/evidence but never retain the generated master key or proxy config.
unset LLAMARACK_LITELLM_MASTER_KEY LLAMARACK_LITELLM_PROXY_KEY
printf 'Compatibility qualification passed for %s\n' "${LLAMARACK_TARGET_ID:-unspecified}"
