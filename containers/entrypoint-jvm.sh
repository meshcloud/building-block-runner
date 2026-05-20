#!/usr/bin/env bash
set -o errexit
set -o errtrace
set -o pipefail
set -o nounset

# don't match glob if no files exist
shopt -s nullglob

CUSTOM_CA_CERTS_PATH="${CUSTOM_CA_CERTS_PATH:-}"
if [[ -n "${CUSTOM_CA_CERTS_PATH}" ]]; then
  CERTS=("${CUSTOM_CA_CERTS_PATH}"/*)
  if [[ ${#CERTS[@]} -gt 0 ]]; then
    # Import custom CA certificates into Java truststore.
    for cert in "${CERTS[@]}"; do
      echo "Importing custom CA certificate into Java truststore: ${cert}"
      keytool -cacerts -storepass changeit -noprompt -trustcacerts -importcert \
        -alias "$(basename "${cert}")" -file "${cert}" >/dev/null 2>&1
    done
  fi
fi

exec "$@"
