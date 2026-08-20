#!/usr/bin/env bash
set -euo pipefail

profile="${1:?cover profile path required}"
config="${2:?coverage config path required}"

if [[ ! -f "${profile}" ]]; then
  echo "coverage profile not found: ${profile}" >&2
  exit 1
fi

threshold="$(awk '/^threshold:/{print $2; exit}' "${config}")"
if [[ -z "${threshold}" ]]; then
  echo "threshold missing from ${config}" >&2
  exit 1
fi

total="$(go tool cover -func="${profile}" | awk '/^total:/{gsub(/%/, "", $3); print $3; exit}')"
if [[ -z "${total}" ]]; then
  echo "unable to parse coverage total from ${profile}" >&2
  exit 1
fi

awk -v total="${total}" -v threshold="${threshold}" 'BEGIN {
  if (total + 0 < threshold + 0) {
    printf "coverage %.1f%% is below threshold %s%%\n", total, threshold
    exit 1
  }
  printf "coverage %.1f%% meets threshold %s%%\n", total, threshold
}'
