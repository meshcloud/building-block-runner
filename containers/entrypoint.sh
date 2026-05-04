#!/usr/bin/env bash
set -o errexit
set -o errtrace
set -o pipefail
set -o nounset

# don't match glob if no files exist
shopt -s nullglob

CUSTOM_CA_CERTS_PATH="${CUSTOM_CA_CERTS_PATH:-}"
CERTS=( "$CUSTOM_CA_CERTS_PATH"/* )
if [[ ${#CERTS[@]} -gt 0 ]]; then
  # Update system certificates
  update-ca-certificates
fi

exec "$@"
