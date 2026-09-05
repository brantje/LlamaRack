#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
resolver="${repo_root}/scripts/release-qualification/resolve-llama-runtime.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mock_bin="${tmpdir}/bin"
mkdir -p "$mock_bin"

cat > "${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${MOCK_CURL_FORBID:-}" == '1' ]]; then
  echo 'curl must not be used in container-current mode' >&2
  exit 99
fi

args="$*"
if [[ "$args" == *'/releases/latest'* ]]; then
  cat <<'JSON'
{"tag_name":"v0.4.0","draft":false,"prerelease":false,"assets":[{"name":"nightly-tag.txt","browser_download_url":"https://example.test/nightly-tag.txt"}]}
JSON
else
  printf 'b10809\n'
fi
EOF
chmod +x "${mock_bin}/curl"

cat > "${mock_bin}/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

state_file="${MOCK_DOCKER_STATE:?}"
log_file="${MOCK_DOCKER_LOG:?}"
mode="${MOCK_DOCKER_MODE:-match}"
format=''
ref=''
prev=''
for arg in "$@"; do
  if [[ "$prev" == '--format' ]]; then
    format="$arg"
  fi
  if [[ "$arg" == ghcr.io/* ]]; then
    ref="$arg"
  fi
  prev="$arg"
done
printf '%s\n' "$ref" >> "$log_file"

if [[ "$mode" == 'hang' ]]; then
  sleep 60
  exit 1
fi

is_version_query=0
if [[ "$format" == *Annotations* || "$format" == *image.version* ]]; then
  is_version_query=1
fi

digest_for() {
  case "$1" in
    ghcr.io/ggml-org/llama.cpp:server)
      printf '"sha256:%064d"\n' 0 | tr '0' 'c'
      ;;
    ghcr.io/ggml-org/llama.cpp:server-cuda)
      printf '"sha256:%064d"\n' 0 | tr '0' 'd'
      ;;
    *)
      return 1
      ;;
  esac
}

cpu_digest_ref='ghcr.io/ggml-org/llama.cpp@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
cuda_digest_ref='ghcr.io/ggml-org/llama.cpp@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'

version_for() {
  case "$1" in
    ghcr.io/ggml-org/llama.cpp:server|"$cpu_digest_ref")
      printf '%s\n' "${MOCK_IMAGE_VERSION:-b10809}"
      ;;
    ghcr.io/ggml-org/llama.cpp:server-cuda|"$cuda_digest_ref")
      printf '%s\n' "${MOCK_IMAGE_VERSION:-b10809}"
      ;;
    *)
      return 1
      ;;
  esac
}

if [[ "$mode" == 'never' ]]; then
  exit 1
fi

if [[ "$mode" == 'lag' ]]; then
  count=0
  if [[ -f "$state_file" ]]; then
    count="$(cat "$state_file")"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$state_file"
  if (( count < 3 )); then
    exit 1
  fi
fi

if [[ "$mode" == 'mismatch-cuda' ]]; then
  if [[ "$is_version_query" == '1' ]]; then
    case "$ref" in
      ghcr.io/ggml-org/llama.cpp:server|"$cpu_digest_ref") printf 'b10795\n' ;;
      ghcr.io/ggml-org/llama.cpp:server-cuda|"$cuda_digest_ref") printf 'b10796\n' ;;
      *) exit 1 ;;
    esac
    exit 0
  fi
  digest_for "$ref" || exit 1
  exit 0
fi

case "$ref" in
  ghcr.io/ggml-org/llama.cpp:server|ghcr.io/ggml-org/llama.cpp:server-cuda|"$cpu_digest_ref"|"$cuda_digest_ref")
    if [[ "$is_version_query" == '1' ]]; then
      version_for "$ref" || exit 1
    else
      digest_for "$ref" || exit 1
    fi
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${mock_bin}/docker"

export MOCK_DOCKER_STATE="${tmpdir}/docker-state"
export MOCK_DOCKER_LOG="${tmpdir}/docker.log"
export PATH="${mock_bin}:${PATH}"

expect_log_refs() {
  local expected="$1"
  cmp -s "$MOCK_DOCKER_LOG" "$expected" || {
    echo 'docker inspect refs did not match' >&2
    diff -u "$expected" "$MOCK_DOCKER_LOG" >&2 || true
    exit 1
  }
}

container_output="${tmpdir}/container.out"
export MOCK_DOCKER_MODE='container'
export MOCK_CURL_FORBID='1'
: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
GITHUB_EVENT_NAME=push \
LLAMA_RUNTIME_RESOLVE_ATTEMPTS=1 \
LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "$container_output" 2>"${tmpdir}/container.err"

grep -qx 'release_tag=container-current' "$container_output"
grep -qx 'build_tag=container-current' "$container_output"
grep -qx 'cpu_image=ghcr.io/ggml-org/llama.cpp@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' "$container_output"
grep -qx 'cuda_image=ghcr.io/ggml-org/llama.cpp@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' "$container_output"
printf '%s\n' \
  'ghcr.io/ggml-org/llama.cpp:server' \
  'ghcr.io/ggml-org/llama.cpp:server-cuda' > "${tmpdir}/container-expected.log"
expect_log_refs "${tmpdir}/container-expected.log"
grep -q 'Resolved current llama.cpp container runtimes by immutable digest.' "${tmpdir}/container.err"

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
unset MOCK_CURL_FORBID
export MOCK_DOCKER_MODE='match'
export MOCK_IMAGE_VERSION='b10809'
success_output="${tmpdir}/success.out"
GITHUB_EVENT_NAME=release \
LLAMA_RUNTIME_RESOLVE_ATTEMPTS=1 \
LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "$success_output" 2>"${tmpdir}/success.err"

grep -qx 'release_tag=v0.4.0' "$success_output"
grep -qx 'build_tag=b10809' "$success_output"
grep -qx 'cpu_image=ghcr.io/ggml-org/llama.cpp@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' "$success_output"
grep -qx 'cuda_image=ghcr.io/ggml-org/llama.cpp@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' "$success_output"
grep -q 'Resolved latest stable llama.cpp v0.4.0 (b10809)' "${tmpdir}/success.err"
printf '%s\n' \
  'ghcr.io/ggml-org/llama.cpp:server' \
  'ghcr.io/ggml-org/llama.cpp:server-cuda' \
  'ghcr.io/ggml-org/llama.cpp@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' \
  'ghcr.io/ggml-org/llama.cpp@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' > "${tmpdir}/match-expected.log"
expect_log_refs "${tmpdir}/match-expected.log"

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
export MOCK_DOCKER_MODE='match'
export MOCK_IMAGE_VERSION='b10795'
unpublished_output="${tmpdir}/unpublished.out"
GITHUB_EVENT_NAME=release \
LLAMA_RUNTIME_RESOLVE_ATTEMPTS=1 \
LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "$unpublished_output" 2>"${tmpdir}/unpublished.err"

grep -qx 'release_tag=b10795' "$unpublished_output"
grep -qx 'build_tag=b10795' "$unpublished_output"
grep -qx 'cpu_image=ghcr.io/ggml-org/llama.cpp@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' "$unpublished_output"
grep -qx 'cuda_image=ghcr.io/ggml-org/llama.cpp@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' "$unpublished_output"
grep -q 'GitHub latest stable llama.cpp v0.4.0 (b10809) is not the current GHCR container runtime' "${tmpdir}/unpublished.err"
if grep -q 'v0.4.0' "$unpublished_output"; then
  echo 'unpublished GitHub stable identity leaked into resolver output' >&2
  exit 1
fi

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
export MOCK_DOCKER_MODE='lag'
export MOCK_IMAGE_VERSION='b10809'
lag_output="${tmpdir}/lag.out"
GITHUB_EVENT_NAME=release \
LLAMA_RUNTIME_RESOLVE_ATTEMPTS=2 \
LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "$lag_output" 2>"${tmpdir}/lag.err"

grep -qx 'release_tag=v0.4.0' "$lag_output"
grep -qx 'build_tag=b10809' "$lag_output"
grep -q 'attempt 1/2' "${tmpdir}/lag.err"

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
export MOCK_DOCKER_MODE='never'
if GITHUB_EVENT_NAME=release \
  LLAMA_RUNTIME_RESOLVE_ATTEMPTS=2 \
  LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "${tmpdir}/failure.out" >"${tmpdir}/failure.stdout" 2>"${tmpdir}/failure.err"; then
  echo 'resolver unexpectedly succeeded when current container runtimes never appeared' >&2
  exit 1
fi
grep -q 'after 2 attempts' "${tmpdir}/failure.err"
grep -q 'ghcr.io/ggml-org/llama.cpp:server' "${tmpdir}/failure.err"

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
export MOCK_DOCKER_MODE='mismatch-cuda'
if GITHUB_EVENT_NAME=release \
  LLAMA_RUNTIME_RESOLVE_ATTEMPTS=1 \
  LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "${tmpdir}/mismatch.out" >"${tmpdir}/mismatch.stdout" 2>"${tmpdir}/mismatch.err"; then
  echo 'resolver unexpectedly succeeded when CPU/CUDA builds differed' >&2
  exit 1
fi
grep -q 'different llama.cpp builds' "${tmpdir}/mismatch.err"

if command -v timeout >/dev/null 2>&1; then
  : > "$MOCK_DOCKER_LOG"
  rm -f "$MOCK_DOCKER_STATE"
  export MOCK_DOCKER_MODE='hang'
  start_ts="$(date +%s)"
  if GITHUB_EVENT_NAME=push \
    MOCK_CURL_FORBID=1 \
    LLAMA_RUNTIME_RESOLVE_ATTEMPTS=1 \
    LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
    LLAMA_RUNTIME_INSPECT_TIMEOUT_SECONDS=1 \
    bash "$resolver" "${tmpdir}/hang.out" >"${tmpdir}/hang.stdout" 2>"${tmpdir}/hang.err"; then
    echo 'resolver unexpectedly succeeded when imagetools inspect hung' >&2
    exit 1
  fi
  elapsed=$(( $(date +%s) - start_ts ))
  if (( elapsed > 10 )); then
    echo "inspect timeout did not fail fast (elapsed ${elapsed}s)" >&2
    exit 1
  fi
  grep -q 'after 1 attempts' "${tmpdir}/hang.err"
  unset MOCK_CURL_FORBID
fi

echo 'resolve-llama-runtime mode and retry tests passed'
