#!/usr/bin/env bash
set -euo pipefail

output_file="${1:-}"
repository="${LLAMA_CPP_REPOSITORY:-ggml-org/llama.cpp}"
api_url="${GITHUB_API_URL:-https://api.github.com}"
resolve_attempts="${LLAMA_RUNTIME_RESOLVE_ATTEMPTS:-20}"
resolve_delay_seconds="${LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS:-30}"
inspect_timeout_seconds="${LLAMA_RUNTIME_INSPECT_TIMEOUT_SECONDS:-20}"

if [[ -n "${LLAMA_RUNTIME_RESOLUTION_MODE:-}" ]]; then
  resolution_mode="$LLAMA_RUNTIME_RESOLUTION_MODE"
elif [[ "${GITHUB_EVENT_NAME:-}" == 'push' ]]; then
  resolution_mode='container'
else
  resolution_mode='release'
fi

for command in curl jq docker; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done

case "$resolution_mode" in
  release|container) ;;
  *) echo "LLAMA_RUNTIME_RESOLUTION_MODE must be release or container: ${resolution_mode}" >&2; exit 1 ;;
esac
if [[ ! "$resolve_attempts" =~ ^[1-9][0-9]*$ ]]; then
  echo "LLAMA_RUNTIME_RESOLVE_ATTEMPTS must be a positive integer: ${resolve_attempts}" >&2
  exit 1
fi
if [[ ! "$resolve_delay_seconds" =~ ^[0-9]+$ ]]; then
  echo "LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS must be a non-negative integer: ${resolve_delay_seconds}" >&2
  exit 1
fi
if [[ ! "$inspect_timeout_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "LLAMA_RUNTIME_INSPECT_TIMEOUT_SECONDS must be a positive integer: ${inspect_timeout_seconds}" >&2
  exit 1
fi

alias_cpu_tag='ghcr.io/ggml-org/llama.cpp:server'
alias_cuda_candidates=(
  'ghcr.io/ggml-org/llama.cpp:server-cuda'
  'ghcr.io/ggml-org/llama.cpp:server-cuda13'
  'ghcr.io/ggml-org/llama.cpp:server-cuda12'
)

inspect_format() {
  local ref="$1" format="$2" output
  local -a cmd=(docker buildx imagetools inspect "$ref" --format "$format")
  if command -v timeout >/dev/null 2>&1; then
    cmd=(timeout --signal=KILL "${inspect_timeout_seconds}s" "${cmd[@]}")
  fi
  output="$("${cmd[@]}" 2>/dev/null)" || return 1
  printf '%s' "$output"
}

pin_digest() {
  local ref="$1" digest
  digest="$(inspect_format "$ref" '{{json .Manifest.Digest}}' | tr -d '"')" || return 1
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
  printf '%s@%s' "${ref%:*}" "$digest"
}

image_build_tag() {
  local ref="$1" version
  version="$(inspect_format "$ref" '{{index .Manifest.Annotations "org.opencontainers.image.version"}}' | tr -d '\r\n[:space:]')" || return 1
  [[ "$version" =~ ^b[0-9]+$ ]] || return 1
  printf '%s' "$version"
}

cpu_image=''
cuda_image=''
selected_cuda_tag=''
for ((attempt = 1; attempt <= resolve_attempts; attempt++)); do
  cpu_image="$(pin_digest "$alias_cpu_tag")" || cpu_image=''

  cuda_image=''
  selected_cuda_tag=''
  for candidate in "${alias_cuda_candidates[@]}"; do
    if resolved="$(pin_digest "$candidate")"; then
      cuda_image="$resolved"
      selected_cuda_tag="$candidate"
      break
    fi
  done

  if [[ -n "$cpu_image" && -n "$cuda_image" ]]; then
    break
  fi

  if (( attempt < resolve_attempts )); then
    echo "llama.cpp current container runtimes are not fully published yet (attempt ${attempt}/${resolve_attempts}); retrying in ${resolve_delay_seconds}s." >&2
    sleep "$resolve_delay_seconds"
  fi
done

if [[ -z "$cpu_image" ]]; then
  echo "Missing or unresolved llama.cpp CPU runtime image after ${resolve_attempts} attempts: ${alias_cpu_tag}" >&2
  exit 1
fi
if [[ -z "$cuda_image" ]]; then
  echo "No supported llama.cpp CUDA runtime image found for the current container aliases after ${resolve_attempts} attempts." >&2
  exit 1
fi

if [[ "$resolution_mode" == 'container' ]]; then
  release_tag='container-current'
  build_tag='container-current'
else
  github_headers=(
    -H 'Accept: application/vnd.github+json'
    -H 'X-GitHub-Api-Version: 2022-11-28'
  )
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    github_headers+=( -H "Authorization: Bearer ${GITHUB_TOKEN}" )
  fi

  release_json="$(curl --fail --silent --show-error \
    "${github_headers[@]}" \
    "${api_url}/repos/${repository}/releases/latest")"

  github_release_tag="$(jq -r '.tag_name // empty' <<<"${release_json}")"
  draft="$(jq -r '.draft // false' <<<"${release_json}")"
  prerelease="$(jq -r '.prerelease // false' <<<"${release_json}")"
  asset_url="$(jq -r '.assets[]? | select(.name == "nightly-tag.txt") | .browser_download_url' <<<"${release_json}" | head -n1)"

  if [[ "$draft" != "false" || "$prerelease" != "false" ]]; then
    echo 'GitHub releases/latest unexpectedly returned a draft or prerelease.' >&2
    exit 1
  fi
  if [[ -z "$github_release_tag" ]]; then
    echo 'Unable to determine the latest stable llama.cpp release tag.' >&2
    exit 1
  fi
  if [[ ! "$github_release_tag" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
    echo "Unsupported llama.cpp release tag: ${github_release_tag}" >&2
    exit 1
  fi
  if [[ -z "$asset_url" ]]; then
    echo "llama.cpp ${github_release_tag} does not expose nightly-tag.txt; cannot resolve an immutable build identifier." >&2
    exit 1
  fi

  github_build_tag="$(curl --fail --silent --show-error --location "$asset_url" | tr -d '\r\n[:space:]')"
  if [[ ! "$github_build_tag" =~ ^b[0-9]+$ ]]; then
    echo "Unexpected llama.cpp build tag: ${github_build_tag}" >&2
    exit 1
  fi

  cpu_build="$(image_build_tag "$alias_cpu_tag")" || {
    echo "Unable to read llama.cpp build identity from ${alias_cpu_tag}." >&2
    exit 1
  }
  cuda_build="$(image_build_tag "$selected_cuda_tag")" || {
    echo "Unable to read llama.cpp build identity from ${selected_cuda_tag}." >&2
    exit 1
  }
  if [[ "$cpu_build" != "$cuda_build" ]]; then
    echo "CPU/CUDA current container runtimes report different llama.cpp builds (${cpu_build} vs ${cuda_build})." >&2
    exit 1
  fi

  build_tag="$cpu_build"
  if [[ "$cpu_build" == "$github_build_tag" ]]; then
    release_tag="$github_release_tag"
    echo "Resolved latest stable llama.cpp ${release_tag} (${build_tag}) from current GHCR container runtimes." >&2
  else
    release_tag="$cpu_build"
    echo "GitHub latest stable llama.cpp ${github_release_tag} (${github_build_tag}) is not the current GHCR container runtime; pinning published ${build_tag} images by digest." >&2
  fi
fi

emit() {
  local key="$1" value="$2"
  if [[ -n "$output_file" ]]; then
    printf '%s=%s\n' "$key" "$value" >> "$output_file"
  else
    printf '%s=%s\n' "$key" "$value"
  fi
}

emit release_tag "$release_tag"
emit build_tag "$build_tag"
emit cpu_image "$cpu_image"
emit cuda_image "$cuda_image"

if [[ "$resolution_mode" == 'container' ]]; then
  echo "Resolved current llama.cpp container runtimes by immutable digest." >&2
fi
