#!/usr/bin/env bash
set -euo pipefail

output_file="${1:-}"
repository="${LLAMA_CPP_REPOSITORY:-ggml-org/llama.cpp}"
api_url="${GITHUB_API_URL:-https://api.github.com}"

for command in curl jq docker; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done

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

cpu_image="ghcr.io/ggml-org/llama.cpp:server-${build_tag}"
if ! docker buildx imagetools inspect "$cpu_image" >/dev/null 2>&1; then
  echo "Missing llama.cpp CPU runtime image: ${cpu_image}" >&2
  exit 1
fi

cuda_image=''
for candidate in \
  "ghcr.io/ggml-org/llama.cpp:server-cuda-${build_tag}" \
  "ghcr.io/ggml-org/llama.cpp:server-cuda13-${build_tag}" \
  "ghcr.io/ggml-org/llama.cpp:server-cuda12-${build_tag}"; do
  if docker buildx imagetools inspect "$candidate" >/dev/null 2>&1; then
    cuda_image="$candidate"
    break
  fi
done
if [[ -z "$cuda_image" ]]; then
  echo "No supported llama.cpp CUDA runtime image found for ${build_tag}." >&2
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
