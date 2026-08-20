#!/usr/bin/env bash
set -euo pipefail

url="${1:-http://127.0.0.1:27000/api/v1/masters}"
attempts="${2:-60}"
sleep_s="${3:-3}"

for i in $(seq 1 "${attempts}"); do
  if curl -fsS --max-time 2 "${url}" >/dev/null 2>&1; then
    echo "yugabyte master ready: ${url}"
    exit 0
  fi
  echo "waiting for YugabyteDB Master (${i}/${attempts}) ${url}"
  sleep "${sleep_s}"
done

echo "timed out waiting for ${url}" >&2
exit 1
