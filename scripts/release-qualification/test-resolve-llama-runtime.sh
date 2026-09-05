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
mode="${MOCK_DOCKER_MODE:-lag}"
ref=''
for arg in "$@"; do
  if [[ "$arg" == ghcr.io/* ]]; then
    ref="$arg"
    break
  fi
done
printf '%s\n' "$ref" >> "$log_file"

if [[ "$mode" == 'never' ]]; then
  exit 1
fi

count=0
if [[ -f "$state_file" ]]; then
  count="$(cat "$state_file")"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$state_file"

case "$count" in
  1|2|3|4)
    exit 1
    ;;
  5)
    printf '"sha256:%064d"\n' 0 | tr '0' 'a'
    ;;
  6)
    printf '"sha256:%064d"\n' 0 | tr '0' 'b'
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${mock_bin}/docker"

success_output="${tmpdir}/success.out"
export MOCK_DOCKER_STATE="${tmpdir}/docker-state"
export MOCK_DOCKER_LOG="${tmpdir}/docker.log"
export MOCK_DOCKER_MODE='lag'
PATH="${mock_bin}:${PATH}" \
LLAMA_RUNTIME_RESOLVE_ATTEMPTS=2 \
LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "$success_output" 2>"${tmpdir}/success.err"

grep -qx 'release_tag=v0.4.0' "$success_output"
grep -qx 'build_tag=b10809' "$success_output"
grep -qx 'cpu_image=ghcr.io/ggml-org/llama.cpp@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' "$success_output"
grep -qx 'cuda_image=ghcr.io/ggml-org/llama.cpp@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' "$success_output"
grep -q 'attempt 1/2' "${tmpdir}/success.err"
if grep -Ev '^ghcr\.io/ggml-org/llama\.cpp:server(-cuda|-cuda13|-cuda12)?-b10809$' "$MOCK_DOCKER_LOG" | grep -q .; then
  echo 'resolver queried a runtime outside the selected b10809 build' >&2
  exit 1
fi

: > "$MOCK_DOCKER_LOG"
rm -f "$MOCK_DOCKER_STATE"
export MOCK_DOCKER_MODE='never'
if PATH="${mock_bin}:${PATH}" \
  LLAMA_RUNTIME_RESOLVE_ATTEMPTS=2 \
  LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS=0 \
  bash "$resolver" "${tmpdir}/failure.out" >"${tmpdir}/failure.stdout" 2>"${tmpdir}/failure.err"; then
  echo 'resolver unexpectedly succeeded when the exact runtime never appeared' >&2
  exit 1
fi

grep -q 'after 2 attempts' "${tmpdir}/failure.err"
if grep -Ev '^ghcr\.io/ggml-org/llama\.cpp:server(-cuda|-cuda13|-cuda12)?-b10809$' "$MOCK_DOCKER_LOG" | grep -q .; then
  echo 'resolver fell back to a runtime outside the selected b10809 build' >&2
  exit 1
fi

echo 'resolve-llama-runtime retry tests passed'
