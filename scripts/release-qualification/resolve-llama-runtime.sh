#!/usr/bin/env bash
set -euo pipefail

output_file="${1:-}"
repository="${LLAMA_CPP_REPOSITORY:-ggml-org/llama.cpp}"
api_url="${GITHUB_API_URL:-https://api.github.com}"
resolve_attempts="${LLAMA_RUNTIME_RESOLVE_ATTEMPTS:-20}"
resolve_delay_seconds="${LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS:-30}"

for command in curl jq docker; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done

if [[ ! "$resolve_attempts" =~ ^[1-9][0-9]*$ ]]; then
  echo "LLAMA_RUNTIME_RESOLVE_ATTEMPTS must be a positive integer: ${resolve_attempts}" >&2
  exit 1
fi
if [[ ! "$resolve_delay_seconds" =~ ^[0-9]+$ ]]; then
  echo "LLAMA_RUNTIME_RESOLVE_DELAY_SECONDS must be a non-negative integer: ${resolve_delay_seconds}" >&2
  exit 1
fi

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

release_tag="$(jq -r '.tag_name // empty' <<<"${release_json}")"
draft="$(jq -r '.draft // false' <<<"${release_json}")"
prerelease="$(jq -r '.prerelease // false' <<<"${release_json}")"
asset_url="$(jq -r '.assets[]? | select(.name == "nightly-tag.txt") | .browser_download_url' <<<"${release_json}" | head -n1)"

if [[ "$draft" != "false" || "$prerelease" != "false" ]]; then
  echo 'GitHub releases/latest unexpectedly returned a draft or prerelease.' >&2
  exit 1
fi
if [[ -z "$release_tag" ]]; then
  echo 'Unable to determine the latest stable llama.cpp release tag.' >&2
  exit 1
fi
if [[ ! "$release_tag" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
  echo "Unsupported llama.cpp release tag: ${release_tag}" >&2
  exit 1
fi
if [[ -z "$asset_url" ]]; then
  echo "llama.cpp ${release_tag} does not expose nightly-tag.txt; cannot resolve an immutable build identifier." >&2
  exit 1
fi

build_tag="$(curl --fail --silent --show-error --location "$asset_url" | tr -d '\r\n[:space:]')"
if [[ ! "$build_tag" =~ ^b[0-9]+$ ]]; then
  echo "Unexpected llama.cpp build tag: ${build_tag}" >&2
  exit 1
fi

pin_digest() {
  local ref="$1" digest
  digest="$(docker buildx imagetools inspect "$ref" --format '{{json .Manifest.Digest}}' 2>/dev/null | tr -d '"')" || return 1
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
  printf '%s@%s' "${ref%:*}" "$digest"
}

cpu_tag="ghcr.io/ggml-org/llama.cpp:server-${build_tag}"
cuda_candidates=(
  "ghcr.io/ggml-org/llama.cpp:server-cuda-${build_tag}"
  "ghcr.io/ggml-org/llama.cpp:server-cuda13-${build_tag}"
  "ghcr.io/ggml-org/llama.cpp:server-cuda12-${build_tag}"
)

cpu_image=''
cuda_image=''
for ((attempt = 1; attempt <= resolve_attempts; attempt++)); do
  cpu_image="$(pin_digest "$cpu_tag")" || cpu_image=''

  cuda_image=''
  for candidate in "${cuda_candidates[@]}"; do
    if resolved="$(pin_digest "$candidate")"; then
      cuda_image="$resolved"
      break
    fi
  done

  if [[ -n "$cpu_image" && -n "$cuda_image" ]]; then
    break
  fi

  if (( attempt < resolve_attempts )); then
    echo "llama.cpp ${build_tag} runtime images are not fully published yet (attempt ${attempt}/${resolve_attempts}); retrying in ${resolve_delay_seconds}s." >&2
    sleep "$resolve_delay_seconds"
  fi
done

if [[ -z "$cpu_image" ]]; then
  echo "Missing or unresolved llama.cpp CPU runtime image after ${resolve_attempts} attempts: ${cpu_tag}" >&2
  exit 1
fi
if [[ -z "$cuda_image" ]]; then
  echo "No supported llama.cpp CUDA runtime image found for ${build_tag} after ${resolve_attempts} attempts." >&2
  exit 1
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

echo "Resolved latest stable llama.cpp ${release_tag} (${build_tag})." >&2
