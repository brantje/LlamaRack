#!/usr/bin/env bash
set -euo pipefail

artifact_dir="${1:?usage: monitor-gpu-soak.sh <artifact-dir>}"
mkdir -p "$artifact_dir"
output="$artifact_dir/manager-resource-samples.tsv"
printf 'timestamp\tgoroutines\trss_kib\n' >"$output"

container=""
for _ in $(seq 1 120); do
  container="$(docker ps --filter 'name=llamarack-gpu-qualification-' --format '{{.Names}}' | head -n1)"
  [[ -n "$container" ]] && break
  sleep 1
done
[[ -n "$container" ]] || { echo "GPU qualification container did not appear" >&2; exit 1; }

port="$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8000/tcp") 0).HostPort}}' "$container")"
[[ "$port" =~ ^[0-9]+$ ]] || { echo "unable to resolve published manager port" >&2; exit 1; }

probe_host="127.0.0.1"
publish_host=""
if [[ -f "$artifact_dir/docker-network.txt" ]]; then
  probe_host="$(awk -F= '/^probe_host=/{print $2; exit}' "$artifact_dir/docker-network.txt")"
  publish_host="$(awk -F= '/^publish_host=/{print $2; exit}' "$artifact_dir/docker-network.txt")"
fi
[[ -n "$probe_host" ]] || probe_host="127.0.0.1"

candidates=()
for host in "$probe_host" "$publish_host" "127.0.0.1"; do
  [[ -n "$host" ]] || continue
  skip=0
  for existing in "${candidates[@]+"${candidates[@]}"}"; do
    if [[ "$existing" == "$host" ]]; then
      skip=1
      break
    fi
  done
  if (( skip )); then
    continue
  fi
  candidates+=("$host")
done

scrape_goroutines() {
  local host metrics value
  for host in "${candidates[@]}"; do
    metrics="$(curl -fsS --connect-timeout 2 --max-time 5 "http://${host}:${port}/metrics" 2>/dev/null || true)"
    [[ -n "$metrics" ]] || continue
    value="$(awk '$1 == "llamarack_manager_goroutines" {print $2; exit}' <<<"$metrics")"
    if [[ -n "$value" ]]; then
      printf '%s\n' "$value"
      return 0
    fi
  done
  return 0
}

while docker inspect "$container" >/dev/null 2>&1; do
  goroutines="$(scrape_goroutines)"
  manager_pid="$(docker exec "$container" sh -c '
    for p in /proc/[0-9]*; do
      [ "$(cat "$p/comm" 2>/dev/null)" = "llamarack" ] && { echo "${p##*/}"; exit 0; }
    done
  ' 2>/dev/null || true)"
  rss_kib=""
  if [[ -n "$manager_pid" ]]; then
    rss_kib="$(docker exec "$container" awk '/^VmRSS:/ {print $2; exit}' "/proc/${manager_pid}/status" 2>/dev/null || true)"
  fi
  if [[ -n "${goroutines:-}" && -n "${rss_kib:-}" ]]; then
    printf '%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$goroutines" "$rss_kib" >>"$output"
  fi
  sleep 2
done
