#!/bin/sh
# Prints one field (clientId or clientSecret) of the hosted-runner API key from the dev config that a local
# meshStack renders when it is built. This lets `task start:runner` log in without keeping the key in this repo.
set -eu

config=$1
field=$2

if [ ! -f "$config" ]; then
  echo "Cannot read the local meshStack API key: $config does not exist." >&2
  echo "Build meshfed-api in ../meshfed-release, or set RUNNER_API_CLIENT_ID and RUNNER_API_CLIENT_SECRET." >&2
  exit 1
fi

sed -n '/meshcloud-hosted-runner:/,/rightPrefixes:/p' "$config" \
  | sed -n "s/^ *$field: *'\{0,1\}\([^']*\)'\{0,1\}$/\1/p"
