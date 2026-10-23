#!/usr/bin/env bash
set -o errexit
set -o errtrace
set -o pipefail
set -o nounset

shopt -s nullglob

CUSTOM_CA_CERTS_PATH="${CUSTOM_CA_CERTS_PATH:-}"
CERTS=( "$CUSTOM_CA_CERTS_PATH"/* )
if [[ ${#CERTS[@]} -gt 0 ]]; then
  update-ca-certificates
fi

exec "$@"
